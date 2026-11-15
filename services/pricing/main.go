// Pricing servisi (ichki): buyurtma narxini tarif bo'yicha hisoblaydi.
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
	"syscall"
	"time"

	"github.com/izgo/pricing/internal/config"
	"github.com/izgo/pricing/internal/httpapi"
	"github.com/izgo/pricing/internal/service"
	"github.com/izgo/pricing/internal/tariff"
	"github.com/izgo/pricing/migrations"
	"github.com/izgo/shared/geo"
	"github.com/izgo/shared/go/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "pricing")

	// distroless'da shell/curl yo'q: docker healthcheck uchun `/pricing healthcheck`.
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
		port = "8014"
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

	tariffs := tariff.NewCache(rdb, tariff.NewStore(pool), cfg.TariffCacheTTL, log)
	// Boshlang'ich tekshiruv: amaldagi tarif yo'q yoki buzuq bo'lsa servis darhol xato bilan to'xtaydi.
	if _, err := tariffs.Active(ctx); err != nil {
		return fmt.Errorf("boshlang'ich tarif: %w", err)
	}

	srv := &http.Server{
		Addr:              net.JoinHostPort("", cfg.Port),
		Handler:           httpapi.New(service.New(tariffs, router), cfg.InternalTokens, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("pricing running", "port", cfg.Port)
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
	pc.MaxConns = 10
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
	// Kesh faqat tezlashtirish: Redis sekin/yo'q bo'lsa har so'rov uzoq kutmasligi uchun qisqa timeoutlar
	// (muvaffaqiyatsiz bo'lsa narx bazadan hisoblanadi).
	opt.DialTimeout, opt.ReadTimeout, opt.WriteTimeout = 500*time.Millisecond, 300*time.Millisecond, 300*time.Millisecond
	opt.MaxRetries = 1
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
