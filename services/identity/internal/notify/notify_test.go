package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const goodKey = "test-good-key"

type captured struct {
	mu      sync.Mutex
	path    string
	headers http.Header
	body    map[string]any
	calls   int
}

// fakeSMS — smsxabarnoma.uz o'rnida: X-API-Key noto'g'ri bo'lsa 401.
func fakeSMS(t *testing.T, c *captured) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.calls++
		c.path = r.URL.Path
		c.headers = r.Header.Clone()
		data, _ := io.ReadAll(r.Body)
		c.body = map[string]any{}
		_ = json.Unmarshal(data, &c.body)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sms/send" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("X-API-Key") != goodKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-1","status":"QUEUED","parts":1,"cost":"60"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fakeResend(t *testing.T, c *captured) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.calls++
		c.path = r.URL.Path
		c.headers = r.Header.Clone()
		data, _ := io.ReadAll(r.Body)
		c.body = map[string]any{}
		_ = json.Unmarshal(data, &c.body)
		if r.Header.Get("Authorization") != "Bearer "+goodKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":"email-1"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type recFallback struct {
	mu         sync.Mutex
	sms, email int
}

func (f *recFallback) SendSMS(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sms++
	return nil
}

func (f *recFallback) SendEmail(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.email++
	return nil
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{
		"+998901234567":      "998901234567",
		"998901234567":       "998901234567",
		" +998 90 123-45-67": "998901234567",
	} {
		got, err := NormalizePhone(in)
		if err != nil || got != want {
			t.Errorf("NormalizePhone(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "+7901234567", "901234567", "+99890123456", "+9989012345678", "+998abc234567"} {
		if _, err := NormalizePhone(bad); !errors.Is(err, ErrInvalidPhone) {
			t.Errorf("NormalizePhone(%q): want ErrInvalidPhone, got %v", bad, err)
		}
	}
}

func TestSMSRealModeSendsRequest(t *testing.T) {
	var c captured
	srv := fakeSMS(t, &c)
	n := New(Config{SMSMode: "real", SMSProviderURL: srv.URL + "/api/v1/", SMSProviderKey: goodKey}, &recFallback{}, quietLog())
	n.now = func() time.Time { return time.UnixMilli(1700000000123) }
	if !n.SMSReal() {
		t.Fatal("real rejim tanlanmadi")
	}
	if err := n.SendSMS(context.Background(), "+998901234567", "1234"); err != nil {
		t.Fatalf("SendSMS: %v", err)
	}
	if c.path != "/api/v1/sms/send" {
		t.Errorf("path = %q", c.path)
	}
	if got := c.headers.Get("X-API-Key"); got != goodKey {
		t.Errorf("X-API-Key = %q", got)
	}
	if got := c.headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if c.body["phone"] != "998901234567" {
		t.Errorf("phone = %v (+ olib tashlanishi kerak)", c.body["phone"])
	}
	if c.body["text"] != "IZGO tasdiqlash kodi: 1234" {
		t.Errorf("text = %v", c.body["text"])
	}
	ext, _ := c.body["externalId"].(string)
	if ext == "" || ext != c.body["idempotencyKey"] {
		t.Errorf("externalId = %q, idempotencyKey = %v (bir xil bo'lishi kerak)", ext, c.body["idempotencyKey"])
	}
	if !strings.HasPrefix(ext, "otp-") || !strings.HasSuffix(ext, "-1700000000123") || strings.Contains(ext, "998901234567") {
		t.Errorf("externalId format: %q", ext)
	}
}

// Real rejim + noto'g'ri kalit -> provayder 401 -> ErrUnauthorized.
func TestSMSRealModeWrongKeyReturns401(t *testing.T) {
	var c captured
	srv := fakeSMS(t, &c)
	n := New(Config{SMSMode: "real", SMSProviderURL: srv.URL + "/api/v1", SMSProviderKey: "wrong-key"}, &recFallback{}, quietLog())
	err := n.SendSMS(context.Background(), "+998901234567", "1234")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Status != http.StatusUnauthorized {
		t.Fatalf("want ProviderError{401}, got %#v", err)
	}
	if strings.Contains(err.Error(), "wrong-key") {
		t.Fatal("xato matnida kalit bor")
	}
}

func TestSMSStatusErrors(t *testing.T) {
	for status, want := range map[int]error{
		http.StatusUnauthorized:    ErrUnauthorized,
		http.StatusPaymentRequired: ErrInsufficientBalance,
		http.StatusTooManyRequests: ErrRateLimited,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		c := &SMSXabarnoma{BaseURL: srv.URL, APIKey: goodKey}
		_, err := c.Send(context.Background(), "+998901234567", "x", "id-1")
		srv.Close()
		if !errors.Is(err, want) {
			t.Errorf("status %d: want %v, got %v", status, want, err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer srv.Close()
	_, err := (&SMSXabarnoma{BaseURL: srv.URL, APIKey: goodKey}).Send(context.Background(), "+998901234567", "x", "id-1")
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Status != http.StatusBadGateway || pe.Err != nil {
		t.Errorf("502: want ProviderError{502}, got %v", err)
	}
}

func TestSMSInvalidPhoneNotSent(t *testing.T) {
	var c captured
	srv := fakeSMS(t, &c)
	n := New(Config{SMSMode: "real", SMSProviderURL: srv.URL + "/api/v1", SMSProviderKey: goodKey}, &recFallback{}, quietLog())
	if err := n.SendSMS(context.Background(), "+79001234567", "1234"); !errors.Is(err, ErrInvalidPhone) {
		t.Fatalf("want ErrInvalidPhone, got %v", err)
	}
	if c.calls != 0 {
		t.Fatal("noto'g'ri telefon provayderga yuborilmasligi kerak")
	}
}

func TestMockModes(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"mock", Config{SMSMode: "mock", SMSProviderKey: goodKey, EmailMode: "mock", EmailAPIKey: goodKey}},
		{"real-but-empty-key", Config{SMSMode: "real", EmailMode: "real"}},
		{"empty-mode", Config{SMSProviderKey: goodKey, EmailAPIKey: goodKey}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c captured
			srv := fakeSMS(t, &c)
			tc.cfg.SMSProviderURL = srv.URL + "/api/v1"
			fb := &recFallback{}
			n := New(tc.cfg, fb, quietLog())
			if n.SMSReal() || n.EmailReal() {
				t.Fatal("mock rejim kutilgan edi")
			}
			if err := n.SendSMS(context.Background(), "+998901234567", "1234"); err != nil {
				t.Fatal(err)
			}
			if err := n.SendEmail(context.Background(), "admin@example.com", "123456"); err != nil {
				t.Fatal(err)
			}
			if fb.sms != 1 || fb.email != 1 || c.calls != 0 {
				t.Fatalf("fallback sms=%d email=%d, provayder chaqiruvlari=%d", fb.sms, fb.email, c.calls)
			}
		})
	}
}

func TestEmailRealModeSendsRequest(t *testing.T) {
	var c captured
	srv := fakeResend(t, &c)
	n := New(Config{EmailMode: "real", EmailProvider: "resend", EmailAPIKey: goodKey, EmailFrom: "onboarding@resend.dev", OTPTTL: 3 * time.Minute},
		&recFallback{}, quietLog())
	n.email.BaseURL = srv.URL
	if err := n.SendEmail(context.Background(), "admin@example.com", "123456"); err != nil {
		t.Fatalf("SendEmail: %v", err)
	}
	if c.path != "/emails" {
		t.Errorf("path = %q", c.path)
	}
	if got := c.headers.Get("Authorization"); got != "Bearer "+goodKey {
		t.Errorf("Authorization = %q", got)
	}
	want := map[string]any{
		"from": "onboarding@resend.dev", "to": "admin@example.com",
		"subject": "IZGO Admin - kirish kodi", "text": "Sizning kodingiz: 123456\nKod 3 daqiqa amal qiladi.",
	}
	for k, v := range want {
		if c.body[k] != v {
			t.Errorf("%s = %#v, want %#v", k, c.body[k], v)
		}
	}
}

func TestEmailRealModeWrongKeyReturns401(t *testing.T) {
	var c captured
	srv := fakeResend(t, &c)
	n := New(Config{EmailMode: "real", EmailAPIKey: "wrong-key", EmailFrom: "x@y.z", OTPTTL: 3 * time.Minute}, &recFallback{}, quietLog())
	n.email.BaseURL = srv.URL
	if err := n.SendEmail(context.Background(), "admin@example.com", "123456"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

// Haqiqiy smsxabarnoma.uz ga noto'g'ri kalit bilan so'rov (SMS yuborilmaydi, pul yechilmaydi).
// Tarmoq kerak, shuning uchun faqat SMS_LIVE_TEST=1 bo'lganda: SMS_LIVE_TEST=1 go test ./internal/notify -run Live
func TestLiveSMSXabarnomaWrongKey(t *testing.T) {
	if os.Getenv("SMS_LIVE_TEST") != "1" {
		t.Skip("SMS_LIVE_TEST=1 berilmagan")
	}
	base := os.Getenv("SMS_PROVIDER_URL")
	if base == "" {
		base = "https://smsxabarnoma.uz/api/v1"
	}
	c := &SMSXabarnoma{BaseURL: base, APIKey: "invalid-key-for-test", HTTP: &http.Client{Timeout: 15 * time.Second}}
	_, err := c.Send(context.Background(), "+998901234567", "IZGO test", ExternalID("test", "live", time.Now()))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want 401 ErrUnauthorized, got %v", err)
	}
}
