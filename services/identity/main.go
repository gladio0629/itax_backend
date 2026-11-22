// Identity servisi: OTP bilan kirish, JWT (Ed25519), profil, admin 2 qadamli kirish.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/izgo/shared/go/migrate"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/izgo/identity/internal/auth"
	"github.com/izgo/identity/internal/cache"
	"github.com/izgo/identity/internal/config"
	"github.com/izgo/identity/internal/events"
	"github.com/izgo/identity/internal/httpapi"
	"github.com/izgo/identity/internal/notify"
	"github.com/izgo/identity/internal/postgres"
	"github.com/izgo/identity/internal/token"
	"github.com/izgo/identity/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "identity")

	// distroless'da shell/curl yo'q: docker healthcheck uchun `/identity healthcheck`.
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
		port = "8010"
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

	dev := cfg.DevOTP()
	if dev.Enabled() {
		// Kod va telefonlar logga yozilmaydi — faqat soni.
		log.Warn("DEV_MODE yoqilgan: ro'yxatdagi telefon/email'lar uchun OTP belgilangan kod (FAQAT dev/test)",
			"app_env", cfg.AppEnv, "test_phones", len(dev.Phones), "test_emails", len(dev.Emails))
	}

	review := cfg.ReviewOTP()
	if review.Enabled() {
		// Kod va telefonlar logga yozilmaydi — faqat soni. Bu qator yo'q bo'lsa: fayllar mount qilinmagan yoki bo'sh.
		log.Info("review akkaunt yoqilgan: ro'yxatdagi telefonlar uchun OTP belgilangan kod, SMS yuborilmaydi",
			"review_phones", len(review.Phones))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	priv, pub, err := token.LoadKeys(cfg.JWTPrivateKeyPath, cfg.JWTPublicKeyPath)
	if err != nil {
		return err
	}
	tokens, err := token.NewManager(priv, pub, cfg.JWTIssuer)
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

	pub2, err := events.Connect(cfg.NATSURL, log)
	if err != nil {
		return err
	}
	defer pub2.Close()

	repo := postgres.NewRepo(pool)
	relay := events.NewRelay(repo, pub2, log)

	// SMS (Eskiz) / email (Resend): SMS_MODE / EMAIL_MODE = real va maxfiy qiymatlar bo'lsa provayder, aks holda logga (mock).
	notifier := notify.New(notify.Config{
		SMSMode: cfg.SMSMode, SMSProviderURL: cfg.SMSProviderURL,
		SMSProviderEmail: cfg.SMSProviderEmail, SMSProviderPassword: cfg.SMSProviderPassword,
		SMSTokens: cache.NewEskizToken(rdb),
		EmailMode: cfg.EmailMode, EmailProvider: cfg.EmailProvider, EmailAPIKey: cfg.EmailAPIKey, EmailFrom: cfg.EmailFrom,
		OTPTTL: cfg.OTPTTL,
	}, auth.LogNotifier{Log: log}, log)

	svc := auth.NewService(auth.Options{
		AccessTTL: cfg.AccessTTL, RefreshTTL: cfg.RefreshTTL, AdminTTL: cfg.AdminTTL,
		OTPTTL: cfg.OTPTTL, OTPResend: cfg.OTPResend, LimitWindow: cfg.OTPLimitWindow,
		OTPMaxAttempts: cfg.OTPMaxAttempts, PhoneLimit: cfg.OTPPhoneLimit, IPLimit: cfg.OTPIPLimit,
		Dev: dev, Review: review,
	}, repo, cache.NewOTP(rdb), cache.NewBlacklist(rdb), tokens, notifier, log)
	svc.OnEvent(relay.Kick)

	if cfg.AdminBootstrapEmail != "" {
		if err := svc.EnsureBootstrapStaff(ctx, cfg.AdminBootstrapEmail, cfg.AdminBootstrapPhone, cfg.AdminBootstrapName); err != nil {
			return fmt.Errorf("bootstrap superadmin: %w", err)
		}
	}

	go relay.Run(ctx)
	go purgeLoop(ctx, repo, log)

	srv := &http.Server{
		Addr:              net.JoinHostPort("", cfg.Port),
		Handler:           httpapi.New(svc, cfg.InternalTokens, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("identity running", "port", cfg.Port)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("to'xtatilmoqda")
	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// connectPG — compose'da postgres hali tayyor bo'lmasligi mumkin: ~30 soniya qayta uriniladi.
func connectPG(ctx context.Context, cfg config.Config, log *slog.Logger) (*pgxpool.Pool, error) {
	// Har doim aniq DSN: ParseConfig("") PG* muhit o'zgaruvchilari va standart fallback'larni meros qilib olardi.
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

func purgeLoop(ctx context.Context, repo *postgres.Repo, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := repo.PurgeExpiredRefresh(ctx); err != nil && ctx.Err() == nil {
			log.Warn("eski refresh tokenlarni tozalab bo'lmadi", "err", err)
		} else if n > 0 {
			log.Info("eski refresh tokenlar tozalandi", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
