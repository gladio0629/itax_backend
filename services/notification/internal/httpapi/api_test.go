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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/izgo/notification/internal/authn"
	"github.com/izgo/notification/internal/httpapi"
	"github.com/izgo/notification/internal/push"
	"github.com/izgo/notification/internal/service"
	"github.com/izgo/notification/internal/sms"
	"github.com/izgo/notification/internal/store"
	"github.com/redis/go-redis/v9"
)

const (
	issuer        = "izgo-identity-test"
	internalToken = "test-internal-token-0123456789"
)

// capture — push va SMS'ni yig'adi (haqiqiy FCM/APNs/SMS yo'q).
type capture struct {
	mu      sync.Mutex
	pushes  []pushed
	unreg   map[string]bool
	smsSent []string
}

type pushed struct {
	Platform, Token string
	Data            map[string]string
}

func (c *capture) Send(_ context.Context, platform, token string, data map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unreg[token] {
		return push.ErrUnregistered
	}
	c.pushes = append(c.pushes, pushed{platform, token, data})
	return nil
}

func (c *capture) pushed() []pushed {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pushed(nil), c.pushes...)
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type env struct {
	t    *testing.T
	mr   *miniredis.Miniredis
	rdb  *redis.Client
	st   *store.Store
	svc  *service.Service
	cap  *capture
	logs *syncBuf
	srv  *httptest.Server
	sk   ed25519.PrivateKey
	now  time.Time
	mu   sync.Mutex
}

type opts struct {
	maxPerUser  int
	revokeCheck time.Duration
	logSMSText  bool
	smsSender   sms.Sender // nil => sms.Mock
	authTimeout time.Duration
	noQueryTok  bool // WS_ALLOW_QUERY_TOKEN=false
	origins     []string
}

func newEnv(t *testing.T, mut ...func(*opts)) *env {
	t.Helper()
	o := opts{maxPerUser: 500, revokeCheck: 100 * time.Millisecond, authTimeout: 5 * time.Second}
	for _, m := range mut {
		m(&o)
	}
	pk, sk, _ := ed25519.GenerateKey(rand.Reader)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	logs := &syncBuf{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	e := &env{t: t, mr: mr, rdb: rdb, cap: &capture{unreg: map[string]bool{}}, logs: logs, sk: sk, now: time.Now().UTC()}
	e.st = store.New(rdb, store.Options{TTL: 30 * 24 * time.Hour, MaxPerUser: o.maxPerUser, MaxDevices: 3})
	e.svc = service.New(e.st, rdb, e.cap, log).WithClock(func() time.Time { e.mu.Lock(); defer e.mu.Unlock(); return e.now })
	var sender sms.Sender = sms.Mock{Log: log, LogText: o.logSMSText}
	if o.smsSender != nil {
		sender = o.smsSender
	}
	api := httpapi.New(e.st, rdb, authn.NewVerifier(pk, issuer, rdb), sender,
		httpapi.Options{InternalTokens: []string{internalToken}, WSMaxConns: 3, WSRevokeCheck: o.revokeCheck, WSPingInterval: time.Minute,
			WSAuthTimeout: o.authTimeout, WSAllowQueryToken: !o.noQueryTok, WSAllowedOrigins: o.origins}, log)
	e.srv = httptest.NewServer(api.Handler())
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) advance(d time.Duration) { e.mu.Lock(); e.now = e.now.Add(d); e.mu.Unlock() }

func (e *env) token(role string, user uuid.UUID, aud string, ttl time.Duration, sid string) string {
	if aud == "" {
		aud = "client"
		if role == "driver" {
			aud = "driver"
		}
	}
	if sid == "" {
		sid = uuid.NewString()
	}
	now := time.Now()
	tok, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": issuer, "sub": user.String(), "aud": []string{aud}, "jti": uuid.NewString(), "typ": "access", "sid": sid, "role": role,
		"iat": now.Unix(), "exp": now.Add(ttl).Unix(),
	}).SignedString(e.sk)
	if err != nil {
		e.t.Fatal(err)
	}
	return tok
}

func (e *env) tok(role string, user uuid.UUID) string { return e.token(role, user, "", time.Hour, "") }

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
}

func (r resp) errKey() string {
	e, _ := r.Body["error"].(map[string]any)
	k, _ := e["key"].(string)
	return k
}

func (e *env) do(method, path string, hdr map[string]string, body any) resp {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
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
	out := resp{Status: res.StatusCode, Raw: raw}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.Body)
	}
	return out
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }
func internal() map[string]string         { return map[string]string{"X-Internal-Token": internalToken} }

// ---- hodisa yasash

func evt(name string, payload map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"event": name, "version": 1, "event_id": uuid.NewString(), "occurred_at": "2026-11-12T10:00:00Z", "payload": payload})
	return b
}

func (e *env) orderCreated(passenger, order uuid.UUID) []byte {
	return evt("order.created", map[string]any{"order_id": order.String(), "passenger_id": passenger.String(), "type": "passenger", "price": 99999,
		"from": map[string]any{"lat": 41.3, "lng": 69.2, "address": "Maxfiy manzil"}, "to": map[string]any{"lat": 41.2, "lng": 69.1}})
}

func (e *env) device(user uuid.UUID, token, platform string) {
	e.t.Helper()
	if r := e.do("POST", "/internal/notification/device", internal(), map[string]any{"user_id": user.String(), "token": token, "platform": platform}); r.Status != 204 {
		e.t.Fatalf("device: %d %s", r.Status, r.Raw)
	}
}

func (e *env) list(tok, q string) resp {
	e.t.Helper()
	return e.do("GET", "/api/v1/notifications"+q, bearer(tok), nil)
}

func ids(r resp) []string {
	var out []string
	for _, x := range r.Body["data"].([]any) {
		out = append(out, x.(map[string]any)["id"].(string))
	}
	return out
}

// ---- WebSocket yordamchilari

func (e *env) wsURL(tok string) string {
	return "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/ws?token=" + tok
}

func (e *env) dial(tok string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return websocket.Dial(ctx, e.wsURL(tok), nil)
}

func (e *env) mustDial(tok string) *websocket.Conn {
	e.t.Helper()
	c, resp, err := e.dial(tok)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		e.t.Fatalf("ws dial: %v (HTTP %d)", err, code)
	}
	e.t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func read(c *websocket.Conn, timeout time.Duration) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	_ = json.Unmarshal(data, &m)
	return m, nil
}
