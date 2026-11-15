package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

func (e *env) wsBase() string { return "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/ws" }

func (e *env) dialOpts(url string, o *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, url, o)
	if err == nil {
		e.t.Cleanup(func() { _ = c.CloseNow() })
	}
	return c, resp, err
}

func status(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// closeErr — keyingi o'qishda kelgan yopish kodi va sababi.
func closeErr(t *testing.T, c *websocket.Conn, within time.Duration) (websocket.StatusCode, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err == nil {
		t.Fatalf("yopilish kutilgan, xabar keldi: %s", data)
	}
	var ce websocket.CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("close frame kelmadi: %v", err)
	}
	return ce.Code, ce.Reason
}

func writeText(t *testing.T, c *websocket.Conn, s string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(s)); err != nil {
		t.Fatal(err)
	}
}

// ---- 1. Subprotokol

func TestWSSubprotocolAuth(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c, resp, err := e.dialOpts(e.wsBase(), &websocket.DialOptions{Subprotocols: []string{"izgo.v1", "bearer." + e.tok("passenger", u)}})
	if err != nil {
		t.Fatalf("dial: %v (HTTP %d)", err, status(resp))
	}
	if got := resp.Header.Values("Sec-WebSocket-Protocol"); len(got) != 1 || got[0] != "izgo.v1" {
		t.Fatalf("javob subprotokoli %q, faqat izgo.v1 kutilgan", got)
	}
	if c.Subprotocol() != "izgo.v1" {
		t.Fatalf("subprotocol %q", c.Subprotocol())
	}
	order := uuid.New()
	_ = e.svc.Handle(ctx, e.orderCreated(u, order))
	if m, err := read(c, 2*time.Second); err != nil || m["type"] != "order.created" || m["order_id"] != order.String() {
		t.Fatalf("hodisa: %v %v", m, err)
	}
	// token loglarga tushmaydi
	if strings.Contains(e.logs.String(), "bearer.") || strings.Contains(e.logs.String(), "eyJ") {
		t.Fatal("token logda")
	}
}

func TestWSSubprotocolRejected(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	good := e.tok("passenger", u)
	cases := map[string][]string{
		"axlat token":    {"izgo.v1", "bearer.abc.def.ghi"},
		"muddati o'tgan": {"izgo.v1", "bearer." + e.token("passenger", u, "", -time.Minute, "")},
		"admin audience": {"izgo.v1", "bearer." + e.token("passenger", u, "admin", time.Hour, "")},
		"izgo.v1 siz":    {"bearer." + good},
		"bo'sh bearer":   {"izgo.v1", "bearer."},
		"ikkita bearer":  {"izgo.v1", "bearer." + good, "bearer." + good},
	}
	for name, protos := range cases {
		c, resp, err := e.dialOpts(e.wsBase(), &websocket.DialOptions{Subprotocols: protos})
		if err == nil {
			_ = c.CloseNow()
			t.Errorf("%s: ulanish ochildi", name)
			continue
		}
		if status(resp) != 401 {
			t.Errorf("%s: HTTP %d, 401 kutilgan", name, status(resp))
		}
	}
	// subprotokol tokeni query'dan ustun: noto'g'ri bearer + to'g'ri query — baribir 401
	if _, resp, err := e.dialOpts(e.wsBase()+"?token="+good, &websocket.DialOptions{Subprotocols: []string{"izgo.v1", "bearer.bad"}}); err == nil || status(resp) != 401 {
		t.Errorf("ustuvorlik: %v HTTP %d", err, status(resp))
	}
}

func TestWSSubprotocolConnectionLimit(t *testing.T) {
	e := newEnv(t)
	o := &websocket.DialOptions{Subprotocols: []string{"izgo.v1", "bearer." + e.tok("passenger", uuid.New())}}
	for i := 0; i < 3; i++ {
		if _, resp, err := e.dialOpts(e.wsBase(), o); err != nil {
			t.Fatalf("%d: %v %d", i, err, status(resp))
		}
	}
	if _, resp, err := e.dialOpts(e.wsBase(), o); err == nil || status(resp) != 403 {
		t.Fatalf("4-ulanish: %v HTTP %d", err, status(resp))
	}
}

// ---- 2. Birinchi xabar

func TestWSFirstMessageAuth(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	c, resp, err := e.dialOpts(e.wsBase(), &websocket.DialOptions{Subprotocols: []string{"izgo.v1"}})
	if err != nil {
		t.Fatalf("dial: %v %d", err, status(resp))
	}
	if resp.Header.Get("Sec-WebSocket-Protocol") != "izgo.v1" {
		t.Fatalf("subprotokol: %q", resp.Header.Get("Sec-WebSocket-Protocol"))
	}
	// auth'dan oldingi hodisa yetkazilmaydi (hali obuna yo'q)
	writeText(t, c, `{"type":"auth","token":"`+e.tok("passenger", u)+`"}`)
	if m, err := read(c, 2*time.Second); err != nil || m["type"] != "auth.ok" || len(m) != 1 {
		t.Fatalf("auth.ok: %v %v", m, err)
	}
	order := uuid.New()
	_ = e.svc.Handle(ctx, e.orderCreated(u, order))
	if m, err := read(c, 2*time.Second); err != nil || m["type"] != "order.created" || m["order_id"] != order.String() {
		t.Fatalf("hodisa: %v %v", m, err)
	}
	if n := len(e.cap.pushed()); n != 0 {
		t.Errorf("WS ulangan, push ketdi: %d", n)
	}
	// autentifikatsiyadan keyin klient ma'lumoti — 1008
	writeText(t, c, `{"type":"auth","token":"x"}`)
	if code, _ := closeErr(t, c, 3*time.Second); code != websocket.StatusPolicyViolation {
		t.Fatalf("yopish kodi %d, 1008 kutilgan", code)
	}
}

// Subprotokolsiz (oddiy klient) ham birinchi xabar bilan ishlaydi; javobda subprotokol yo'q.
func TestWSFirstMessageWithoutSubprotocol(t *testing.T) {
	e := newEnv(t)
	c, resp, err := e.dialOpts(e.wsBase(), nil)
	if err != nil {
		t.Fatalf("dial: %v %d", err, status(resp))
	}
	if resp.Header.Get("Sec-WebSocket-Protocol") != "" {
		t.Fatalf("subprotokol: %q", resp.Header.Get("Sec-WebSocket-Protocol"))
	}
	writeText(t, c, `{"type":"auth","token":"`+e.tok("driver", uuid.New())+`"}`)
	if m, err := read(c, 2*time.Second); err != nil || m["type"] != "auth.ok" {
		t.Fatalf("auth.ok: %v %v", m, err)
	}
}

func TestWSFirstMessageFailures(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	sid := uuid.NewString()
	e.mr.Set("bl:sid:"+sid, "1")
	cases := map[string]struct {
		frame  string
		code   websocket.StatusCode
		reason string
	}{
		"axlat token":            {`{"type":"auth","token":"abc.def.ghi"}`, 4401, "token_invalid"},
		"muddati o'tgan":         {`{"type":"auth","token":"` + e.token("passenger", u, "", -time.Minute, "") + `"}`, 4401, "token_invalid"},
		"bekor qilingan sessiya": {`{"type":"auth","token":"` + e.token("passenger", u, "", time.Hour, sid) + `"}`, 4401, "token_invalid"},
		"JSON emas":              {`hello`, 1008, "auth_malformed"},
		"type noto'g'ri":         {`{"type":"subscribe","token":"` + e.tok("passenger", u) + `"}`, 1008, "auth_malformed"},
		"token yo'q":             {`{"type":"auth"}`, 1008, "auth_malformed"},
	}
	for name, tc := range cases {
		c, resp, err := e.dialOpts(e.wsBase(), &websocket.DialOptions{Subprotocols: []string{"izgo.v1"}})
		if err != nil {
			t.Fatalf("%s: dial %v %d", name, err, status(resp))
		}
		writeText(t, c, tc.frame)
		if code, reason := closeErr(t, c, 3*time.Second); code != tc.code || reason != tc.reason {
			t.Errorf("%s: %d %q, %d %q kutilgan", name, code, reason, tc.code, tc.reason)
		}
	}
	// binar xabar — 1008
	c, _, err := e.dialOpts(e.wsBase(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = c.Write(wctx, websocket.MessageBinary, []byte(`{"type":"auth","token":"x"}`))
	cancel()
	if code, reason := closeErr(t, c, 3*time.Second); code != 1008 || reason != "auth_malformed" {
		t.Errorf("binar: %d %q", code, reason)
	}
}

func TestWSFirstMessageTimeout(t *testing.T) {
	e := newEnv(t, func(o *opts) { o.authTimeout = 300 * time.Millisecond })
	c, resp, err := e.dialOpts(e.wsBase(), &websocket.DialOptions{Subprotocols: []string{"izgo.v1"}})
	if err != nil {
		t.Fatalf("dial: %v %d", err, status(resp))
	}
	start := time.Now()
	code, reason := closeErr(t, c, 3*time.Second)
	if code != 4401 || reason != "auth_timeout" {
		t.Fatalf("%d %q, 4401 auth_timeout kutilgan", code, reason)
	}
	if d := time.Since(start); d < 200*time.Millisecond || d > 2*time.Second {
		t.Errorf("timeout vaqti %v", d)
	}
}

func TestWSFirstMessageConnectionLimit(t *testing.T) {
	e := newEnv(t)
	tok := e.tok("passenger", uuid.New())
	for i := 0; i < 3; i++ {
		e.mustDial(tok)
	}
	c, _, err := e.dialOpts(e.wsBase(), nil)
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, c, `{"type":"auth","token":"`+tok+`"}`)
	if code, reason := closeErr(t, c, 3*time.Second); code != 4403 || reason != "ws_connection_limit" {
		t.Fatalf("%d %q, 4403 ws_connection_limit kutilgan", code, reason)
	}
}

// ---- 3. Eski ?token=

func TestWSQueryTokenFlag(t *testing.T) {
	e := newEnv(t)
	tok := e.tok("passenger", uuid.New())
	c, resp, err := e.dialOpts(e.wsBase()+"?token="+tok, &websocket.DialOptions{Subprotocols: []string{"izgo.v1"}})
	if err != nil {
		t.Fatalf("ruxsat bor, lekin ochilmadi: %v %d", err, status(resp))
	}
	if resp.Header.Get("Sec-WebSocket-Protocol") != "izgo.v1" {
		t.Errorf("subprotokol: %q", resp.Header.Get("Sec-WebSocket-Protocol"))
	}
	_ = c.CloseNow()
	logs := e.logs.String()
	if !strings.Contains(logs, "legacy_query_token_total=1") {
		t.Errorf("deprecation logi yo'q: %s", logs)
	}
	if strings.Contains(logs, tok) {
		t.Fatal("token logga tushdi")
	}

	off := newEnv(t, func(o *opts) { o.noQueryTok = true })
	tok = off.tok("passenger", uuid.New())
	if c, resp, err := off.dialOpts(off.wsBase()+"?token="+tok, nil); err == nil {
		_ = c.CloseNow()
		t.Fatal("WS_ALLOW_QUERY_TOKEN=false, lekin ?token= qabul qilindi")
	} else if status(resp) != 401 {
		t.Fatalf("HTTP %d, 401 kutilgan", status(resp))
	}
	// boshqa usullar ishlayveradi
	if _, resp, err := off.dialOpts(off.wsBase(), &websocket.DialOptions{Subprotocols: []string{"izgo.v1", "bearer." + tok}}); err != nil {
		t.Fatalf("subprotokol: %v %d", err, status(resp))
	}
}

// ---- 4. Origin

func TestWSOrigin(t *testing.T) {
	e := newEnv(t, func(o *opts) { o.origins = []string{"izgo-client.example.app", "localhost:*"} })
	tok := e.tok("passenger", uuid.New())
	dial := func(origin string) int {
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		c, resp, err := e.dialOpts(e.wsBase(), &websocket.DialOptions{Subprotocols: []string{"izgo.v1", "bearer." + tok}, HTTPHeader: h})
		if err != nil {
			return status(resp)
		}
		_ = c.Close(websocket.StatusNormalClosure, "")
		time.Sleep(50 * time.Millisecond) // slot bo'shasin
		return 101
	}
	for origin, want := range map[string]int{
		"":                                101, // mobil ilova: Origin yo'q
		"https://izgo-client.example.app": 101,
		"http://localhost:5173":           101,
		"https://evil.example.com":        403,
		"https://izgo-client.example.app.evil.com": 403,
	} {
		if got := dial(origin); got != want {
			t.Errorf("Origin %q: %d, %d kutilgan", origin, got, want)
		}
	}

	// default (bo'sh ro'yxat): faqat Origin host == Host
	d := newEnv(t)
	h := http.Header{"Origin": []string{"https://izgo-client.example.app"}}
	if _, resp, err := d.dialOpts(d.wsBase(), &websocket.DialOptions{HTTPHeader: h}); err == nil || status(resp) != 403 {
		t.Errorf("default ro'yxat: %v %d", err, status(resp))
	}
}
