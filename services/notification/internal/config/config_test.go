package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func baseEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"REDIS_URL": "redis://r:6379", "NATS_URL": "nats://n:4222", "JWT_PUBLIC_KEY_PATH": "/k/pub.pem",
		"INTERNAL_TOKENS": "0123456789abcdef0123", "SMS_MODE": "",
	} {
		t.Setenv(k, v)
	}
	for _, k := range []string{"WS_AUTH_TIMEOUT", "WS_ALLOW_QUERY_TOKEN", "WS_ALLOWED_ORIGINS"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k) // t.Setenv cleanup'da asl holatini tiklaydi
	}
}

func TestWSDefaults(t *testing.T) {
	baseEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.WSAuthTimeout != 5*time.Second || !c.WSAllowQueryToken || len(c.WSAllowedOrigins) != 0 {
		t.Fatalf("default: %v %v %q", c.WSAuthTimeout, c.WSAllowQueryToken, c.WSAllowedOrigins)
	}
}

func TestWSAllowedOrigins(t *testing.T) {
	baseEnv(t)
	t.Setenv("WS_ALLOWED_ORIGINS", " izgo-client.example.app, localhost:* ,")
	t.Setenv("WS_ALLOW_QUERY_TOKEN", "false")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.WSAllowedOrigins) != 2 || c.WSAllowedOrigins[1] != "localhost:*" || c.WSAllowQueryToken {
		t.Fatalf("%q %v", c.WSAllowedOrigins, c.WSAllowQueryToken)
	}
	for _, bad := range []string{"*", "izgo[.app"} {
		t.Setenv("WS_ALLOWED_ORIGINS", bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WS_ALLOWED_ORIGINS") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestWSAuthTimeoutPositive(t *testing.T) {
	baseEnv(t)
	t.Setenv("WS_AUTH_TIMEOUT", "0s")
	if _, err := Load(); err == nil {
		t.Fatal("WS_AUTH_TIMEOUT=0 qabul qilindi")
	}
}

// Bo'sh qiymat (compose'da `${X:-}`) — parse xatosi, servis ishga tushmaydi. Compose'da default bilan yozilsin:
// `${WS_ALLOW_QUERY_TOKEN:-true}`, `${WS_AUTH_TIMEOUT:-5s}`. WS_ALLOWED_ORIGINS bo'sh bo'lishi mumkin.
func TestWSEmptyValues(t *testing.T) {
	for _, k := range []string{"WS_ALLOW_QUERY_TOKEN", "WS_AUTH_TIMEOUT"} {
		baseEnv(t)
		t.Setenv(k, "")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("bo'sh %s: %v", k, err)
		}
	}
	baseEnv(t)
	t.Setenv("WS_ALLOWED_ORIGINS", "")
	if c, err := Load(); err != nil || len(c.WSAllowedOrigins) != 0 {
		t.Errorf("bo'sh WS_ALLOWED_ORIGINS: %v %q", err, c.WSAllowedOrigins)
	}
}
