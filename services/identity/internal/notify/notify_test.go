package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestMockModes(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"mock", Config{SMSMode: "mock", SMSProviderEmail: eskizEmail, SMSProviderPassword: eskizPassword, EmailMode: "mock", EmailAPIKey: goodKey}},
		{"real-but-empty-key", Config{SMSMode: "real", EmailMode: "real"}},
		{"real-but-no-password", Config{SMSMode: "real", SMSProviderEmail: eskizEmail}},
		{"empty-mode", Config{SMSProviderEmail: eskizEmail, SMSProviderPassword: eskizPassword, EmailAPIKey: goodKey}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeEskiz(t)
			tc.cfg.SMSProviderURL = f.srv.URL + "/api"
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
			if logins, sends := f.counts(); fb.sms != 1 || fb.email != 1 || logins+sends != 0 {
				t.Fatalf("fallback sms=%d email=%d, provayder chaqiruvlari=%d", fb.sms, fb.email, logins+sends)
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
