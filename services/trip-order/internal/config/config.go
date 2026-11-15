// Package config — servis sozlamalari (faqat muhit o'zgaruvchilaridan).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Port string `envconfig:"PORT" default:"8012"`

	DBHost     string `envconfig:"DB_HOST" default:"postgres"`
	DBPort     int    `envconfig:"DB_PORT" default:"5432"`
	DBUser     string `envconfig:"DB_USER" default:"trip_order_svc"`
	DBPassword string `envconfig:"DB_PASSWORD"`
	DBName     string `envconfig:"DB_NAME" default:"trip_order_db"`
	DBSSLMode  string `envconfig:"DB_SSLMODE" default:"disable"`

	RedisURL string `envconfig:"REDIS_URL" required:"true"`
	NATSURL  string `envconfig:"NATS_URL" required:"true"`

	// Identity chiqargan access tokenlarni tekshirish uchun (faqat public kalit).
	JWTPublicKeyPath string `envconfig:"JWT_PUBLIC_KEY_PATH" required:"true"`
	JWTIssuer        string `envconfig:"JWT_ISSUER" default:"izgo-identity"`

	// Haydovchi topilmasa buyurtma shu muddatdan keyin expired bo'ladi.
	SearchTimeout time.Duration `envconfig:"SEARCH_TIMEOUT" default:"2m"`
	// Taklifni qabul qilish SETNX qulfining muddati.
	AcceptLockTTL time.Duration `envconfig:"ACCEPT_LOCK_TTL" default:"30s"`
	// Rejalashtirilgan buyurtma eng ko'pi bilan qancha oldin berilishi mumkin.
	MaxScheduleAhead time.Duration `envconfig:"MAX_SCHEDULE_AHEAD" default:"720h"`
	SweepInterval    time.Duration `envconfig:"SWEEP_INTERVAL" default:"5s"`

	// F-24: faol buyurtmalar limiti.
	PassengerActiveLimit int `envconfig:"PASSENGER_ACTIVE_LIMIT" default:"3"`
	DriverActiveLimit    int `envconfig:"DRIVER_ACTIVE_LIMIT" default:"1"`

	// Ichki servislar. URL bo'sh bo'lsa shu servis uchun mock ishlaydi (lokal/test), berilsa — real HTTP.
	PricingURL  string `envconfig:"PRICING_URL"`
	MatchingURL string `envconfig:"MATCHING_URL"`
	WalletURL   string `envconfig:"WALLET_URL"`
	// X-Internal-Token sirlari (vergul bilan bir nechta — almashtirish uchun); chiqishda birinchisi yuboriladi.
	// Kamida bitta URL berilgan bo'lsa majburiy.
	InternalTokens string `envconfig:"INTERNAL_TOKENS"`
	// Ichki chaqiruv uchun umumiy timeout (ulanish + javob).
	ExternalTimeout time.Duration `envconfig:"EXTERNAL_TIMEOUT" default:"3s"`
}

// InternalToken — chiqish so'rovlariga qo'yiladigan token (INTERNAL_TOKENS dagi birinchi bo'sh bo'lmagan qiymat).
func (c Config) InternalToken() string {
	for _, t := range strings.Split(c.InternalTokens, ",") {
		if t = strings.TrimSpace(t); t != "" {
			return t
		}
	}
	return ""
}

// usesRealServices — kamida bitta ichki servis real HTTP orqali chaqiriladimi.
func (c Config) usesRealServices() bool {
	for _, u := range []string{c.PricingURL, c.MatchingURL, c.WalletURL} {
		if strings.TrimSpace(u) != "" {
			return true
		}
	}
	return false
}

func Load() (Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

func (c Config) validate() error {
	var errs []error
	for name, v := range map[string]string{
		"DB_PASSWORD": c.DBPassword, "REDIS_URL": c.RedisURL, "NATS_URL": c.NATSURL, "JWT_PUBLIC_KEY_PATH": c.JWTPublicKeyPath,
	} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s bo'sh bo'lmasligi kerak", name))
		}
	}
	for name, v := range map[string]time.Duration{
		"SEARCH_TIMEOUT": c.SearchTimeout, "ACCEPT_LOCK_TTL": c.AcceptLockTTL,
		"MAX_SCHEDULE_AHEAD": c.MaxScheduleAhead, "SWEEP_INTERVAL": c.SweepInterval, "EXTERNAL_TIMEOUT": c.ExternalTimeout,
	} {
		if v <= 0 {
			errs = append(errs, fmt.Errorf("%s musbat bo'lishi kerak", name))
		}
	}
	if c.PassengerActiveLimit < 1 || c.DriverActiveLimit < 1 {
		errs = append(errs, errors.New("faol buyurtmalar limiti >= 1 bo'lishi kerak"))
	}
	for name, v := range map[string]string{"PRICING_URL": c.PricingURL, "MATCHING_URL": c.MatchingURL, "WALLET_URL": c.WalletURL} {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, fmt.Errorf("%s: http(s)://host[:port] ko'rinishida bo'lishi kerak", name))
		}
	}
	if c.usesRealServices() {
		// Token yo'q bo'lsa har bir chaqiruv 403 bo'lardi — ishga tushishda to'xtaymiz.
		if c.InternalToken() == "" {
			errs = append(errs, errors.New("INTERNAL_TOKENS: PRICING_URL/MATCHING_URL/WALLET_URL berilganda bo'sh bo'lmasligi kerak"))
		}
		for _, t := range strings.Split(c.InternalTokens, ",") {
			if t = strings.TrimSpace(t); t != "" && len(t) < 16 {
				errs = append(errs, errors.New("INTERNAL_TOKENS: har bir qiymat kamida 16 belgi bo'lishi kerak"))
				break
			}
		}
	}
	return errors.Join(errs...)
}
