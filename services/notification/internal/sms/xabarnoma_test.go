package sms

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{"+998901234567": "998901234567", "998901234567": "998901234567"} {
		if got, err := NormalizePhone(in); err != nil || got != want {
			t.Errorf("NormalizePhone(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "+7901234567", "901234567", "+99890123456"} {
		if _, err := NormalizePhone(bad); !errors.Is(err, ErrInvalidPhone) {
			t.Errorf("NormalizePhone(%q): %v", bad, err)
		}
	}
}

func TestUseReal(t *testing.T) {
	for _, c := range []struct {
		mode, key string
		want      bool
	}{{"real", "k", true}, {"REAL", "k", true}, {"real", "", false}, {"real", "  ", false}, {"mock", "k", false}, {"", "k", false}} {
		if got := UseReal(c.mode, c.key); got != c.want {
			t.Errorf("UseReal(%q,%q) = %v", c.mode, c.key, got)
		}
	}
}

func TestXabarnomaRequestShape(t *testing.T) {
	var hdr http.Header
	var body map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr, path = r.Header.Clone(), r.URL.Path
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		_, _ = w.Write([]byte(`{"id":"m1","status":"QUEUED","parts":1,"cost":"60"}`))
	}))
	defer srv.Close()
	x := Xabarnoma{BaseURL: srv.URL + "/", APIKey: "k-1", Now: func() time.Time { return time.UnixMilli(42) }}
	if err := x.Send(context.Background(), "+998901234567", "salom"); err != nil {
		t.Fatal(err)
	}
	if path != "/sms/send" || hdr.Get("X-API-Key") != "k-1" || hdr.Get("Content-Type") != "application/json" {
		t.Errorf("path=%q headers=%v", path, hdr)
	}
	ext, _ := body["externalId"].(string)
	if body["phone"] != "998901234567" || body["text"] != "salom" || ext != body["idempotencyKey"] ||
		!strings.HasPrefix(ext, "sms-") || !strings.HasSuffix(ext, "-42") {
		t.Errorf("body = %v", body)
	}
}

func TestXabarnomaStatusErrors(t *testing.T) {
	for status, want := range map[int]error{401: ErrUnauthorized, 402: ErrInsufficientBalance, 429: ErrRateLimited} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		err := Xabarnoma{BaseURL: srv.URL, APIKey: "secret-key"}.Send(context.Background(), "+998901234567", "x")
		srv.Close()
		if !errors.Is(err, want) {
			t.Errorf("%d: want %v, got %v", status, want, err)
		}
		if err != nil && strings.Contains(err.Error(), "secret-key") {
			t.Errorf("xatoda kalit bor: %v", err)
		}
	}
}

// Haqiqiy smsxabarnoma.uz ga noto'g'ri kalit bilan (SMS yuborilmaydi). Faqat SMS_LIVE_TEST=1 da.
func TestLiveXabarnomaWrongKey(t *testing.T) {
	if os.Getenv("SMS_LIVE_TEST") != "1" {
		t.Skip("SMS_LIVE_TEST=1 berilmagan")
	}
	base := os.Getenv("SMS_PROVIDER_URL")
	if base == "" {
		base = "https://smsxabarnoma.uz/api/v1"
	}
	x := Xabarnoma{BaseURL: base, APIKey: "invalid-key-for-test", HTTP: &http.Client{Timeout: 15 * time.Second}}
	if err := x.Send(context.Background(), "+998901234567", "IZGO test"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want 401 ErrUnauthorized, got %v", err)
	}
}
