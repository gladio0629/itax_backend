// Package testenv — testlar uchun: haqiqiy PostgreSQL (embedded), miniredis, to'liq HTTP stek.
// Faqat *_test.go fayllaridan import qilinadi.
//
//	PRICING_TEST_DSN=postgres://...  — tayyor server (CREATEDB huquqi kerak); har test uchun alohida baza
//	PRICING_TEST_PG=off              — DB testlarini o'tkazib yuborish
//	(default)                        — embedded-postgres (~/.cache/identity-embedded-pg); yuklab bo'lmasa testlar skip.
package testenv

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/izgo/pricing/internal/httpapi"
	"github.com/izgo/pricing/internal/service"
	"github.com/izgo/pricing/internal/tariff"
	"github.com/izgo/pricing/migrations"
	"github.com/izgo/shared/geo"
	"github.com/izgo/shared/go/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const Token = "test-internal-token-0123456789"

var (
	pgOnce   sync.Once
	pgAdmin  string
	pgErr    error
	pgStop   func()
	dbSerial atomic.Int64
)

func Main(m *testing.M) int {
	code := m.Run()
	if pgStop != nil {
		pgStop()
	}
	return code
}

func startPG() {
	if dsn := os.Getenv("PRICING_TEST_DSN"); dsn != "" {
		pgAdmin = dsn
		return
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		pgErr = err
		return
	}
	port := uint32(l.Addr().(*net.TCPAddr).Port)
	_ = l.Close()
	dir, err := os.MkdirTemp("", "pricing-pg-*")
	if err != nil {
		pgErr = err
		return
	}
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Port(port).RuntimePath(dir + "/run").DataPath(dir + "/data").
		BinariesPath(os.Getenv("HOME") + "/.cache/identity-embedded-pg").Logger(io.Discard))
	if err := pg.Start(); err != nil {
		pgErr = err
		return
	}
	pgStop = func() { _ = pg.Stop(); _ = os.RemoveAll(dir) }
	pgAdmin = fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/postgres?sslmode=disable", port)
}

type Env struct {
	T      *testing.T
	Pool   *pgxpool.Pool
	MR     *miniredis.Miniredis
	RDB    *redis.Client
	Cache  *tariff.Cache
	Server *httptest.Server
}

// New — alohida baza (migratsiyalar, jumladan boshlang'ich tarif bilan) va alohida Redis.
func New(t *testing.T) *Env {
	t.Helper()
	if os.Getenv("PRICING_TEST_PG") == "off" {
		t.Skip("PRICING_TEST_PG=off")
	}
	pgOnce.Do(startPG)
	if pgErr != nil {
		t.Skipf("PostgreSQL ishga tushmadi (DB testlari o'tkazib yuborildi): %v", pgErr)
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, pgAdmin)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("t%d_%d", os.Getpid(), dbSerial.Add(1))
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(pgAdmin)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Run(ctx, pool, migrations.FS, migrations.LockID); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	mr := miniredis.RunT(t)
	redis.SetLogger(quietLogger{}) // Redis o'chirilgan testlarda "dial" shovqini bo'lmasin
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cache := tariff.NewCache(rdb, tariff.NewStore(pool), 5*time.Minute, log)
	router, _ := geo.NewHaversineRouter(1.3, 30)
	srv := httptest.NewServer(httpapi.New(service.New(cache, router), []string{Token, "old-rotating-token-9876543210"}, log).Handler())
	t.Cleanup(srv.Close)
	return &Env{T: t, Pool: pool, MR: mr, RDB: rdb, Cache: cache, Server: srv}
}

type quietLogger struct{}

func (quietLogger) Printf(context.Context, string, ...interface{}) {}
