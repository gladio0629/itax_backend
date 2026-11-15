// Notification servisi: push (mock), SMS (mock), bildirishnomalar ro'yxati, WebSocket. Redis asosiy baza; NATS hodisalarini faqat tinglaydi.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/izgo/notification/internal/authn"
	"github.com/izgo/notification/internal/bus"
	"github.com/izgo/notification/internal/config"
	"github.com/izgo/notification/internal/events"
	"github.com/izgo/notification/internal/httpapi"
	"github.com/izgo/notification/internal/push"
	"github.com/izgo/notification/internal/service"
	"github.com/izgo/notification/internal/sms"
	"github.com/izgo/notification/internal/store"
	"github.com/redis/go-redis/v9"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "notification")

	// distroless'da shell/curl yo'q: docker healthcheck uchun `/notification healthcheck`.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if err := run(log); err != nil {
		log.Error("servis to'xtadi", "err", err)
		os.Exit(1)
	}
}

func healthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8016"
	}
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pub, err := authn.LoadPublicKey(cfg.JWTPublicKeyPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb, err := connectRedis(ctx, cfg.RedisURL, log)
	if err != nil {
		return err
	}
	defer rdb.Close()
	nats, err := bus.Connect(cfg.NATSURL, log)
	if err != nil {
		return err
	}
	defer nats.Close()

	st := store.New(rdb, store.Options{TTL: cfg.NotificationTTL, MaxPerUser: cfg.MaxPerUser, MaxDevices: cfg.MaxDevices})
	// Push hozircha mock (log); real FCM/APNs: push.go oxiridagi izoh. SMS pastda (SMS_MODE).
	svc := service.New(st, rdb, push.Mock{Log: log}, log)

	// Faqat tinglaydi, hech narsa chiqarmaydi. Har subject uchun alohida durable consumer.
	var subs []bus.Sub
	for subject, name := range events.Subjects {
		subs = append(subs, bus.Sub{Subject: subject, Durable: "notification-" + strings.ReplaceAll(name, ".", "-"), Handle: svc.Handle})
	}
	go nats.Run(ctx, subs...)

	// SMS: SMS_MODE=real va SMS_PROVIDER_KEY bo'lsa smsxabarnoma.uz, aks holda Mock (log).
	var smsSender sms.Sender = sms.Mock{Log: log, LogText: cfg.MockLogSMSParams}
	if sms.UseReal(cfg.SMSMode, cfg.SMSProviderKey) {
		smsSender = sms.Xabarnoma{BaseURL: cfg.SMSProviderURL, APIKey: cfg.SMSProviderKey, Log: log}
		log.Info("SMS provayder: real (smsxabarnoma.uz)")
	} else {
		if strings.EqualFold(strings.TrimSpace(cfg.SMSMode), sms.ModeReal) {
			log.Warn("SMS_MODE=real, lekin SMS_PROVIDER_KEY bo'sh: SMS mock rejimda (logga yoziladi)")
		}
		log.Info("SMS provayder: mock")
	}

	api := httpapi.New(st, rdb, authn.NewVerifier(pub, cfg.JWTIssuer, rdb), smsSender,
		httpapi.Options{InternalTokens: cfg.InternalTokens, WSMaxConns: cfg.WSMaxConns, WSRevokeCheck: cfg.WSRevokeCheck, WSPingInterval: cfg.WSPingInterval}, log)
	// WriteTimeout qo'yilmaydi: WebSocket uzoq yashaydi (har bir yozuvga alohida timeout bor).
	srv := &http.Server{
		Addr:              net.JoinHostPort("", cfg.Port),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("notification running", "port", cfg.Port)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("to'xtatilmoqda")
	shut, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func connectRedis(ctx context.Context, url string, log *slog.Logger) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis url: %w", err)
	}
	// Joylashuv har 10 soniyada ko'p haydovchidan keladi: Redis sekin bo'lsa so'rovlar uzoq osilib qolmasin.
	opt.DialTimeout, opt.ReadTimeout, opt.WriteTimeout = 2*time.Second, time.Second, time.Second
	rdb := redis.NewClient(opt)
	var lastErr error
	for i := 0; i < 30; i++ {
		if lastErr = rdb.Ping(ctx).Err(); lastErr == nil {
			return rdb, nil
		}
		log.Warn("redis hali tayyor emas", "err", lastErr)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("redis: %w", lastErr)
}
