// Trip & Order servisi: buyurtmalar, takliflar, safarlar, baholar.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/izgo/shared/go/migrate"
	"github.com/izgo/shared/go/secretenv"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/izgo/shared/go/authn"
	"github.com/izgo/shared/go/pii"
	"github.com/izgo/trip-order/internal/config"
	"github.com/izgo/trip-order/internal/events"
	"github.com/izgo/trip-order/internal/external"
	"github.com/izgo/trip-order/internal/guard"
	"github.com/izgo/trip-order/internal/httpapi"
	"github.com/izgo/trip-order/internal/service"
	"github.com/izgo/trip-order/internal/store"
	"github.com/izgo/trip-order/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{ReplaceAttr: pii.ReplaceAttr})).With("service", "trip-order")

	// distroless'da shell/curl yo'q: docker healthcheck uchun `/trip-order healthcheck`.
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
		port = "8012"
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
	// F-16: Docker secrets (SECRETS_DIR, default /run/secrets/izgo) — config o'qilishidan OLDIN
	secrets, err := secretenv.LoadDefault()
	if err != nil {
		return err
	}
	if len(secrets) > 0 {
		log.Info("sirlar fayldan yuklandi", "names", secrets)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pub, err := authn.LoadPublicKey(cfg.JWTPublicKeyPath)
	if err != nil {
		return err
	}
	pool, err := connectPG(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate.Run(ctx, pool, migrations.FS, migrations.LockID); err != nil {
		return err
	}
	rdb, err := connectRedis(ctx, cfg.RedisURL, log)
	if err != nil {
		return err
	}
	defer rdb.Close()
	nats, err := events.Connect(cfg.NATSURL, log)
	if err != nil {
		return err
	}
	defer nats.Close()

	st := store.New(pool)
	relay := events.NewRelay(st, nats, log)

	// Pricing, Wallet va Matching: *_URL berilgan bo'lsa real HTTP (X-Internal-Token bilan), aks holda mock.
	ext := external.New(external.Endpoints{
		PricingURL: cfg.PricingURL, MatchingURL: cfg.MatchingURL, WalletURL: cfg.WalletURL,
		Token: cfg.InternalToken(), Timeout: cfg.ExternalTimeout,
	}, external.RedisMatcher{RDB: rdb})
	log.Info("ichki servislar", "pricing_real", ext.Real["pricing"], "matching_real", ext.Real["matching"], "wallet_real", ext.Real["wallet"])
	svc := service.New(service.Options{
		SearchTimeout: cfg.SearchTimeout, AcceptLockTTL: cfg.AcceptLockTTL, MaxScheduleAhead: cfg.MaxScheduleAhead,
		MinScheduleRange: cfg.MinScheduleRange, MaxScheduleRange: cfg.MaxScheduleRange, QuoteTTL: cfg.QuoteTTL,
		PassengerActiveLimit: cfg.PassengerActiveLimit, DriverActiveLimit: cfg.DriverActiveLimit,
	}, st, guard.New(rdb), ext.Pricer, ext.Wallet, ext.Matcher, log)
	svc.OnEvent(relay.Kick)

	go relay.Run(ctx)
	go svc.RunSweeper(ctx, cfg.SweepInterval)

	srv := &http.Server{
		Addr:              net.JoinHostPort("", cfg.Port),
		Handler:           httpapi.New(svc, authn.NewVerifier(pub, cfg.JWTIssuer, rdb), log).WithInternalTokens(cfg.InternalTokenList()).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("trip-order running", "port", cfg.Port)
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

// connectPG — compose'da postgres hali tayyor bo'lmasligi mumkin: ~30 soniya qayta uriniladi.
func connectPG(ctx context.Context, cfg config.Config, log *slog.Logger) (*pgxpool.Pool, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		quote(cfg.DBHost), cfg.DBPort, quote(cfg.DBUser), quote(cfg.DBPassword), quote(cfg.DBName), cfg.DBSSLMode)
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pg config: %w", err)
	}
	pc.MaxConns = 20
	var lastErr error
	for i := 0; i < 30; i++ {
		pool, err := pgxpool.NewWithConfig(ctx, pc)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		lastErr = err
		log.Warn("postgres hali tayyor emas", "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("postgres: %w", lastErr)
}

func quote(s string) string {
	out := []byte{'\''}
	for _, r := range s {
		if r == '\'' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, string(r)...)
	}
	return string(append(out, '\''))
}

func connectRedis(ctx context.Context, url string, log *slog.Logger) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis url: %w", err)
	}
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
