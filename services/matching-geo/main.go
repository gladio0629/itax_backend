// Matching & Geo servisi (ichki): haydovchi joylashuvi (Redis GEO), radius qidiruvi, holat, marshrut.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/izgo/shared/go/authn"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/izgo/matching-geo/internal/bus"
	"github.com/izgo/matching-geo/internal/config"
	"github.com/izgo/matching-geo/internal/events"
	"github.com/izgo/matching-geo/internal/httpapi"
	"github.com/izgo/matching-geo/internal/service"
	"github.com/izgo/matching-geo/internal/store"
	"github.com/izgo/shared/geo"
	"github.com/redis/go-redis/v9"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "matching-geo")

	// distroless'da shell/curl yo'q: docker healthcheck uchun `/matching-geo healthcheck`.
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
		port = "8013"
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
	router, err := geo.NewHaversineRouter(cfg.RoadFactor, cfg.AvgSpeedKMH)
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

	pub, err := authn.LoadPublicKey(cfg.JWTPublicKeyPath)
	if err != nil {
		return err
	}

	st := store.New(rdb, store.Options{LocationTTL: cfg.LocationTTL, BusyTTL: cfg.BusyTTL})
	svc := service.New(st, router, nats, service.Options{
		MaxSpeedKMH: cfg.MaxSpeedKMH, MinJumpM: cfg.MinJumpM, DefaultRadiusM: cfg.DefaultRadiusM, MaxRadiusM: cfg.MaxRadiusM,
		MaxCandidates: cfg.MaxCandidates, OfferTTL: cfg.OfferTTL,
	}, log)

	// order.accepted -> haydovchi busy; trip.completed -> available (Trip & Order hodisalari).
	go nats.Run(ctx,
		bus.Sub{Subject: events.SubjectOrderAccepted, Durable: "matching-geo-order-accepted", Handle: svc.OnOrderAccepted},
		bus.Sub{Subject: events.SubjectTripCompleted, Durable: "matching-geo-trip-completed", Handle: svc.OnTripCompleted},
	)
	go sweepLoop(ctx, svc, log)

	srv := &http.Server{
		Addr:              net.JoinHostPort("", cfg.Port),
		Handler:           httpapi.New(svc, cfg.InternalTokens, authn.NewVerifier(pub, cfg.JWTIssuer, rdb), cfg.MaxRadiusM, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("matching-geo running", "port", cfg.Port)
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

func sweepLoop(ctx context.Context, svc *service.Service, log *slog.Logger) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := svc.Sweep(ctx); err != nil {
				if ctx.Err() == nil {
					log.Warn("geo-to'plamni tozalab bo'lmadi", "err", err)
				}
			} else if n > 0 {
				log.Info("harakatsiz haydovchilar geo-to'plamdan olib tashlandi", "count", n)
			}
		}
	}
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
