package config

import (
	"os"
	"strings"
	"testing"
)

// baseEnv — majburiy qiymatlar; ularsiz Load boshqa sabab bilan yiqilib, testlar "bo'sh" o'tib ketardi.
func baseEnv(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{
		"DB_PASSWORD": "x", "REDIS_URL": "redis://r:6379", "NATS_URL": "nats://n:4222",
		"JWT_PRIVATE_KEY_PATH": "/k/priv.pem", "JWT_PUBLIC_KEY_PATH": "/k/pub.pem",
		"INTERNAL_TOKENS": "0123456789abcdef0123",
		"APP_ENV":         "", "SMS_MODE": "", "EMAIL_MODE": "",
		"DEV_MODE": "false", "DEV_OTP_CODE": "", "DEV_TEST_PHONES": "", "DEV_TEST_EMAILS": "",
	} {
		t.Setenv(k, v)
	}
}

func devEnv(t *testing.T) {
	t.Helper()
	baseEnv(t)
	t.Setenv("DEV_MODE", "true")
	t.Setenv("DEV_OTP_CODE", "424242")
	t.Setenv("DEV_TEST_PHONES", "+998900000001, +998900000002,")
	t.Setenv("DEV_TEST_EMAILS", " QA@izgo.uz ")
}

func TestLoadBaseline(t *testing.T) {
	baseEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DevMode || c.DevOTP().Enabled() {
		t.Fatal("dev rejim default o'chiq bo'lishi kerak")
	}

	devEnv(t)
	t.Setenv("APP_ENV", "development")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	d := c.DevOTP()
	if !d.Enabled() || d.Code != "424242" || len(d.Phones) != 2 || len(d.Emails) != 1 {
		t.Fatalf("dev: %+v", d)
	}
	if _, ok := d.Phones["+998900000002"]; !ok {
		t.Fatal("telefon trim qilinmadi")
	}
	if _, ok := d.Emails["qa@izgo.uz"]; !ok {
		t.Fatal("email normallashtirilmadi")
	}
}

func TestAppEnvDefault(t *testing.T) {
	baseEnv(t)
	_ = os.Unsetenv("APP_ENV") // t.Setenv cleanup'da asl holatini tiklaydi
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.AppEnv != "development" {
		t.Fatalf("APP_ENV default: %q", c.AppEnv)
	}
}

func TestDevModeRefused(t *testing.T) {
	for name, tc := range map[string]struct{ key, val, want string }{
		"production":        {"APP_ENV", "production", "APP_ENV=production"},
		"prod":              {"APP_ENV", "Prod", "APP_ENV=production"},
		"sms real":          {"SMS_MODE", "real", "SMS_MODE=real"},
		"sms real kalitsiz": {"SMS_MODE", " REAL ", "SMS_MODE=real"},
		"email real":        {"EMAIL_MODE", "real", "EMAIL_MODE=real"},
		"kod qisqa":         {"DEV_OTP_CODE", "12345", "DEV_OTP_CODE"},
		"kod uzun":          {"DEV_OTP_CODE", "1234567", "DEV_OTP_CODE"},
		"kod harf":          {"DEV_OTP_CODE", "12a456", "DEV_OTP_CODE"},
		"kod yo'q":          {"DEV_OTP_CODE", "", "DEV_OTP_CODE"},
		"telefon formati":   {"DEV_TEST_PHONES", "998900000001", "DEV_TEST_PHONES"},
	} {
		t.Run(name, func(t *testing.T) {
			devEnv(t)
			t.Setenv(tc.key, tc.val)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("xato kutilgan (%s): %v", tc.want, err)
			}
		})
	}
}

// DEV_MODE=false: noto'g'ri dev qiymatlari va production ham xato bermaydi, ro'yxat e'tiborsiz.
func TestDevModeOffIgnoresDevSettings(t *testing.T) {
	devEnv(t)
	t.Setenv("DEV_MODE", "false")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SMS_MODE", "real")
	t.Setenv("DEV_OTP_CODE", "1")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if d := c.DevOTP(); d.Enabled() || len(d.Phones) != 0 || len(d.Emails) != 0 {
		t.Fatalf("DEV_MODE=false, lekin dev yoqildi: %+v", d)
	}
}

// Bo'sh DEV_MODE="" (masalan compose'da `${DEV_MODE:-}`) — parse xatosi: servis ishga tushmaydi (fail closed).
// Compose'da `${DEV_MODE:-false}` ishlatilsin.
func TestDevModeEmptyValueFailsStartup(t *testing.T) {
	baseEnv(t)
	t.Setenv("DEV_MODE", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DEV_MODE") {
		t.Fatalf("bo'sh DEV_MODE: %v", err)
	}
}
