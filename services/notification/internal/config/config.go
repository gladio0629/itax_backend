// Package config — servis sozlamalari (faqat muhit o'zgaruvchilaridan).
package config

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Port string `envconfig:"PORT" default:"8016"`

	RedisURL string `envconfig:"REDIS_URL" required:"true"`
	NATSURL  string `envconfig:"NATS_URL" required:"true"`

	// Foydalanuvchi tokenlari (Identity public kaliti) — /api/v1/notifications va /ws uchun.
	JWTPublicKeyPath string `envconfig:"JWT_PUBLIC_KEY_PATH" required:"true"`
	JWTIssuer        string `envconfig:"JWT_ISSUER" default:"izgo-identity"`

	// Ichki API (/internal/*) uchun umumiy sir; vergul bilan bir nechta (almashtirish davrida). Har biri >= 16 belgi.
	InternalTokens []string `envconfig:"INTERNAL_TOKENS" required:"true"`

	NotificationTTL time.Duration `envconfig:"NOTIFICATION_TTL" default:"720h"` // 30 kun
	MaxPerUser      int           `envconfig:"MAX_NOTIFICATIONS_PER_USER" default:"500"`
	MaxDevices      int           `envconfig:"MAX_DEVICES_PER_USER" default:"10"`

	// F-19
	WSMaxConns     int           `envconfig:"WS_MAX_CONNS" default:"3"`
	WSRevokeCheck  time.Duration `envconfig:"WS_REVOKE_CHECK" default:"30s"`
	WSPingInterval time.Duration `envconfig:"WS_PING_INTERVAL" default:"25s"`
	// WSAuthTimeout — birinchi xabar bilan autentifikatsiya ({"type":"auth","token":...}) uchun kutish vaqti.
	WSAuthTimeout time.Duration `envconfig:"WS_AUTH_TIMEOUT" default:"5s"`
	// WSAllowQueryToken — eski `GET /ws?token=` usuli (token Kong access loglariga tushadi). Frontend
	// Sec-WebSocket-Protocol yoki auth xabariga o'tgach false qilinadi; false bo'lsa ?token= so'rovi 401.
	WSAllowQueryToken bool `envconfig:"WS_ALLOW_QUERY_TOKEN" default:"true"`
	// WSAllowedOrigins — boshqa domendagi brauzer ilovalari uchun ruxsat etilgan Origin host naqshlari
	// (path.Match, katta-kichik harf farqsiz, port bilan: "izgo-client.example.app", "localhost:*").
	// Bo'sh — faqat Origin host == Host. Origin sarlavhasiz so'rovlar (mobil ilovalar) har doim o'tadi.
	WSAllowedOrigins []string `envconfig:"WS_ALLOWED_ORIGINS"`

	// Mock SMS: parametrlarni (masalan OTP kodni) logga yozish — FAQAT dev uchun.
	MockLogSMSParams bool `envconfig:"MOCK_LOG_SMS_PARAMS" default:"false"`

	// SMS provayder (smsxabarnoma.uz). SMS_MODE=real va kalit bo'lsa yuboriladi, aks holda Mock (log).
	SMSMode        string `envconfig:"SMS_MODE" default:"mock"`
	SMSProviderURL string `envconfig:"SMS_PROVIDER_URL" default:"https://smsxabarnoma.uz/api/v1"`
	SMSProviderKey string `envconfig:"SMS_PROVIDER_KEY"`
}

func Load() (Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	var errs []error
	for n, v := range map[string]string{"REDIS_URL": c.RedisURL, "NATS_URL": c.NATSURL, "JWT_PUBLIC_KEY_PATH": c.JWTPublicKeyPath} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s bo'sh bo'lmasligi kerak", n))
		}
	}
	if len(c.InternalTokens) == 0 {
		errs = append(errs, errors.New("INTERNAL_TOKENS bo'sh bo'lmasligi kerak"))
	}
	for _, t := range c.InternalTokens {
		if len(strings.TrimSpace(t)) < 16 {
			errs = append(errs, errors.New("INTERNAL_TOKENS: har bir qiymat kamida 16 belgi bo'lishi kerak"))
			break
		}
	}
	c.WSAllowedOrigins = cleanList(c.WSAllowedOrigins)
	for _, p := range c.WSAllowedOrigins {
		if _, err := path.Match(p, ""); err != nil || p == "*" {
			errs = append(errs, fmt.Errorf("WS_ALLOWED_ORIGINS: noto'g'ri naqsh %q (\"*\" taqiqlangan)", p))
		}
	}
	if c.NotificationTTL <= 0 || c.MaxPerUser < 1 || c.MaxDevices < 1 || c.WSMaxConns < 1 || c.WSRevokeCheck <= 0 || c.WSPingInterval <= 0 || c.WSAuthTimeout <= 0 {
		errs = append(errs, errors.New("TTL, limitlar va intervallar musbat bo'lishi kerak"))
	}
	switch strings.ToLower(strings.TrimSpace(c.SMSMode)) {
	case "", "mock": // bo'sh = mock
	case "real":
		if strings.TrimSpace(c.SMSProviderURL) == "" {
			errs = append(errs, errors.New("SMS_MODE=real uchun SMS_PROVIDER_URL kerak"))
		}
	default:
		errs = append(errs, errors.New("SMS_MODE faqat mock yoki real bo'lishi mumkin"))
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

// cleanList — vergul bilan ajratilgan qiymatlar: bo'sh joylar olib tashlanadi, bo'sh elementlar tashlab yuboriladi.
func cleanList(in []string) []string {
	var out []string
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
