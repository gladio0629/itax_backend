package httpapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/izgo/identity/internal/auth"
	"github.com/izgo/identity/internal/cache"
	"github.com/izgo/identity/internal/httpapi"
	"github.com/izgo/identity/internal/memrepo"
	"github.com/izgo/identity/internal/token"
	"github.com/izgo/shared/roles"
	"github.com/redis/go-redis/v9"
)

// testInternalToken — testlardagi INTERNAL_TOKENS qiymati.
const testInternalToken = "test-internal-token-0123456789"

type env struct {
	t      *testing.T
	srv    *httptest.Server
	mr     *miniredis.Miniredis
	repo   *memrepo.Repo
	tokens *token.Manager
	n      *notifier
}

type notifier struct {
	mu    sync.Mutex
	sms   map[string]string
	email map[string]string
}

func (n *notifier) SendSMS(_ context.Context, phone, code string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sms[phone] = code
	return nil
}

func (n *notifier) SendEmail(_ context.Context, email, code string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.email[email] = code
	return nil
}

func (n *notifier) smsCode(phone string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.sms[phone]
}

func (n *notifier) emailCode(email string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.email[email]
}

func newEnv(t *testing.T, mut ...func(*auth.Options)) *env {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := token.NewManager(priv, pub, "izgo-identity-test")
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	opt := auth.Options{
		AccessTTL: 15 * time.Minute, RefreshTTL: 7 * 24 * time.Hour, AdminTTL: time.Hour,
		OTPTTL: 3 * time.Minute, OTPResend: time.Minute, LimitWindow: time.Hour,
		OTPMaxAttempts: 3, PhoneLimit: 5, IPLimit: 20, PhoneBlock: time.Hour,
	}
	for _, m := range mut {
		m(&opt)
	}
	repo := memrepo.New()
	n := &notifier{sms: map[string]string{}, email: map[string]string{}}
	svc := auth.NewService(opt, repo, cache.NewOTP(rdb), cache.NewBlacklist(rdb), tokens, n, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(httpapi.New(svc, []string{testInternalToken}, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, mr: mr, repo: repo, tokens: tokens, n: n}
}

type resp struct {
	Status int
	Header http.Header
	Body   map[string]any
}

func (r resp) errKey() string {
	e, _ := r.Body["error"].(map[string]any)
	k, _ := e["key"].(string)
	return k
}

func (r resp) errParam(name string) any {
	e, _ := r.Body["error"].(map[string]any)
	p, _ := e["params"].(map[string]any)
	return p[name]
}

func (r resp) str(k string) string { s, _ := r.Body[k].(string); return s }

// do — JSON so'rov yuboradi. body: map yoki xom string; hdr: ixtiyoriy sarlavhalar.
func (e *env) do(method, path string, body any, hdr map[string]string) resp {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewBufferString(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Header: res.Header}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.Body); err != nil {
			e.t.Fatalf("javob JSON emas (%d): %q", res.StatusCode, raw)
		}
	}
	return out
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

// login — to'liq OTP oqimi; (access, refresh, userID) qaytaradi.
func (e *env) login(phone string, hdr map[string]string) (access, refresh string, id uuid.UUID) {
	e.t.Helper()
	if r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phone}, hdr); r.Status != 202 {
		e.t.Fatalf("otp/request: %d %v", r.Status, r.Body)
	}
	r := e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phone, "code": e.n.smsCode(phone)}, hdr)
	if r.Status != 200 {
		e.t.Fatalf("otp/verify: %d %v", r.Status, r.Body)
	}
	c, err := e.tokens.Parse(r.str("access_token"), token.Access, token.AudClient, token.AudDriver)
	if err != nil {
		e.t.Fatal(err)
	}
	return r.str("access_token"), r.str("refresh_token"), uuid.MustParse(c.Subject)
}

// addStaff — admin testlari uchun xodim qo'shadi. email == "" — email'siz (operator).
func (e *env) addStaff(email, phone string, role roles.Role, active bool) {
	e.t.Helper()
	var ep *string
	if email != "" {
		ep = &email
	}
	if err := e.repo.EnsureStaff(context.Background(), auth.Staff{ID: uuid.New(), Email: ep, Phone: phone, FullName: "Test", Role: role, Active: active}); err != nil {
		e.t.Fatal(err)
	}
}
