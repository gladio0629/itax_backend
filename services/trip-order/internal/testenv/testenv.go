// Package testenv — testlar uchun to'liq stek: haqiqiy PostgreSQL (embedded), miniredis, boshqariladigan soat,
// token imzolash va HTTP handler. Faqat *_test.go fayllaridan import qilinadi.
//
// PostgreSQL manbai:
//
//	TRIPORDER_TEST_DSN=postgres://...  — tayyor server (CREATEDB huquqi kerak); har test uchun alohida baza ochiladi
//	TRIPORDER_TEST_PG=off              — DB testlarini o'tkazib yuborish
//	(default)                          — embedded-postgres; birinchi marta binary yuklaydi (~/.cache/identity-embedded-pg)
//	                                     va yuklab bo'lmasa DB testlari skip qilinadi.
package testenv

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"github.com/izgo/shared/go/migrate"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/izgo/shared/go/authn"
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

const issuer = "izgo-identity-test"

var (
	pgOnce   sync.Once
	pgAdmin  string // admin DSN (bazasiz qism)
	pgErr    error
	pgStop   func()
	dbSerial atomic.Int64
	sk       ed25519.PrivateKey
	pk       ed25519.PublicKey
	keyOnce  sync.Once
)

// Main — TestMain ichida: os.Exit(testenv.Main(m)).
func Main(m *testing.M) int {
	code := m.Run()
	if pgStop != nil {
		pgStop()
	}
	return code
}

func startPG() {
	if dsn := os.Getenv("TRIPORDER_TEST_DSN"); dsn != "" {
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
	dir, err := os.MkdirTemp("", "triporder-pg-*")
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

// Clock — qo'lda boshqariladigan soat.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// FakeWallet — F-13 testlari uchun: ro'yxatdagi haydovchilar qarz chegarasida.
type FakeWallet struct {
	mu      sync.Mutex
	blocked map[uuid.UUID]int64
}

func (w *FakeWallet) Block(id uuid.UUID, limit int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blocked[id] = limit
}

func (w *FakeWallet) Unblock(id uuid.UUID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.blocked, id)
}

func (w *FakeWallet) CheckDriver(_ context.Context, id uuid.UUID) (external.WalletStatus, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if l, ok := w.blocked[id]; ok {
		return external.WalletStatus{LimitReached: true, Limit: l}, nil
	}
	return external.WalletStatus{}, nil
}

type Option func(*service.Options)

func PassengerLimit(n int) Option { return func(o *service.Options) { o.PassengerActiveLimit = n } }

type Env struct {
	T      *testing.T
	Pool   *pgxpool.Pool
	Store  *store.Store
	MR     *miniredis.Miniredis
	RDB    *redis.Client
	Guard  *guard.Guard
	Svc    *service.Service
	Clock  *Clock
	Wallet *FakeWallet
	Server *httptest.Server
	Opt    service.Options
}

// New — yangi izolyatsiyalangan stek (alohida baza, alohida Redis). PG bo'lmasa test skip qilinadi.
func New(t *testing.T, opts ...Option) *Env {
	t.Helper()
	if os.Getenv("TRIPORDER_TEST_PG") == "off" {
		t.Skip("TRIPORDER_TEST_PG=off")
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
	cfg.MaxConns = 30
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Run(ctx, pool, migrations.FS, migrations.LockID); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	keyOnce.Do(func() { pk, sk, _ = ed25519.GenerateKey(rand.Reader) })

	clock := &Clock{t: time.Now().UTC().Truncate(time.Microsecond)}
	opt := service.Options{
		SearchTimeout: 2 * time.Minute, AcceptLockTTL: 30 * time.Second, MaxScheduleAhead: 720 * time.Hour,
		PassengerActiveLimit: 3, DriverActiveLimit: 1,
	}
	for _, o := range opts {
		o(&opt)
	}
	st := store.New(pool)
	g := guard.New(rdb)
	wallet := &FakeWallet{blocked: map[uuid.UUID]int64{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(opt, st, g, external.MockPricer{}, wallet, external.RedisMatcher{RDB: rdb}, log).WithClock(clock.Now)

	srv := httptest.NewServer(httpapi.New(svc, authn.NewVerifier(pk, issuer, rdb), log).Handler())
	t.Cleanup(srv.Close)
	return &Env{T: t, Pool: pool, Store: st, MR: mr, RDB: rdb, Guard: g, Svc: svc, Clock: clock, Wallet: wallet, Server: srv, Opt: opt}
}

// Token — Identity formatidagi access token.
func (e *Env) Token(role string, id uuid.UUID) string {
	return e.token(role, id, "", time.Hour, nil)
}

// TokenWith — sid, muddat va audience'ni boshqarish uchun.
func (e *Env) token(role string, id uuid.UUID, aud string, ttl time.Duration, sid *string) string {
	if aud == "" {
		aud = "client"
		if role == "driver" {
			aud = "driver"
		}
	}
	s := uuid.NewString()
	if sid != nil {
		s = *sid
	}
	now := time.Now()
	c := jwt.MapClaims{
		"iss": issuer, "sub": id.String(), "aud": []string{aud}, "jti": uuid.NewString(), "typ": "access", "sid": s, "role": role,
		"iat": now.Unix(), "exp": now.Add(ttl).Unix(),
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c).SignedString(sk)
	if err != nil {
		e.T.Fatal(err)
	}
	return tok
}

func (e *Env) TokenAud(role string, id uuid.UUID, aud string) string {
	return e.token(role, id, aud, time.Hour, nil)
}
func (e *Env) TokenExpired(role string, id uuid.UUID) string {
	return e.token(role, id, "", -time.Minute, nil)
}
func (e *Env) TokenSID(role string, id uuid.UUID, sid string) string {
	return e.token(role, id, "", time.Hour, &sid)
}

// AddDrivers — mock matching: bu haydovchilarga keyingi buyurtmalar uchun taklif yaratiladi.
func (e *Env) AddDrivers(ids ...uuid.UUID) {
	for _, id := range ids {
		e.MR.SAdd(external.MatchingMockKey, id.String())
	}
}

// Outbox — events_outbox qatorlari (subject, payload) yaratilish tartibida.
func (e *Env) Outbox() []events.Event {
	rows, err := e.Pool.Query(context.Background(), `SELECT id, subject, payload, created_at FROM events_outbox ORDER BY created_at, id`)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	var out []events.Event
	for rows.Next() {
		var ev events.Event
		if err := rows.Scan(&ev.ID, &ev.Subject, &ev.Payload, &ev.CreatedAt); err != nil {
			e.T.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

// Client — oddiy HTTP mijoz.
var Client = &http.Client{Timeout: 30 * time.Second}
