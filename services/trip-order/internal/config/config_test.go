package config

import (
	"strings"
	"testing"
	"time"
)

func base() Config {
	return Config{
		DBPassword: "x", RedisURL: "redis://r", NATSURL: "nats://n", JWTPublicKeyPath: "/k",
		SearchTimeout: time.Minute, AcceptLockTTL: time.Second, MaxScheduleAhead: time.Hour, SweepInterval: time.Second,
		ExternalTimeout: 3 * time.Second, PassengerActiveLimit: 3, DriverActiveLimit: 1,
		MinScheduleRange: 15 * time.Minute, MaxScheduleRange: 168 * time.Hour, QuoteTTL: 10 * time.Minute,
	}
}

func TestValidateExternal(t *testing.T) {
	const tok = "test-internal-token-0123456789"
	for _, tc := range []struct {
		name    string
		mut     func(*Config)
		wantErr string
	}{
		{"mock'lar: token shart emas", func(*Config) {}, ""},
		{"real + token", func(c *Config) { c.PricingURL, c.InternalTokens = "http://pricing:8014", tok }, ""},
		{"real, token yo'q", func(c *Config) { c.WalletURL = "http://wallet:8015" }, "INTERNAL_TOKENS"},
		{"real, faqat vergul", func(c *Config) { c.MatchingURL, c.InternalTokens = "http://m:1", " , " }, "INTERNAL_TOKENS"},
		{"qisqa token", func(c *Config) { c.PricingURL, c.InternalTokens = "http://p:1", tok+",short" }, "16 belgi"},
		{"noto'g'ri URL", func(c *Config) { c.PricingURL, c.InternalTokens = "pricing:8014", tok }, "PRICING_URL"},
		{"timeout 0", func(c *Config) { c.ExternalTimeout = 0 }, "EXTERNAL_TIMEOUT"},
		{"quote ttl 0", func(c *Config) { c.QuoteTTL = 0 }, "QUOTE_TTL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mut(&c)
			err := c.validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("kutilmagan xato: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("xato = %v, kutilgan %q", err, tc.wantErr)
			}
		})
	}
}

func TestInternalToken(t *testing.T) {
	c := Config{InternalTokens: " , new-token-0123456789abc , old-token-0123456789abc"}
	if got := c.InternalToken(); got != "new-token-0123456789abc" {
		t.Errorf("token = %q", got)
	}
	if got := (Config{}).InternalToken(); got != "" {
		t.Errorf("bo'sh: %q", got)
	}
}
