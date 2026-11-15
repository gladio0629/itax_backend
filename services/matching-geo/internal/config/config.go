// Package config — servis sozlamalari (faqat muhit o'zgaruvchilaridan).
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Port string `envconfig:"PORT" default:"8013"`

	RedisURL string `envconfig:"REDIS_URL" required:"true"`
	NATSURL  string `envconfig:"NATS_URL" required:"true"`

	// Ichki servislararo chaqiruv uchun umumiy sir (vergul bilan bir nechta — almashtirish davrida). Har biri >= 16 belgi.
	InternalTokens []string `envconfig:"INTERNAL_TOKENS" required:"true"`

	// Public API (POST /api/v1/location/update, Kong orqali): Identity access tokeni Ed25519 public kalit bilan tekshiriladi.
	JWTPublicKeyPath string `envconfig:"JWT_PUBLIC_KEY_PATH" required:"true"`
	JWTIssuer        string `envconfig:"JWT_ISSUER" default:"izgo-identity"`

	// Joylashuv shuncha vaqt yangilanmasa haydovchi xaritadan o'chadi.
	LocationTTL time.Duration `envconfig:"LOCATION_TTL" default:"2m"`
	// "busy" holati xavfsizlik chegarasi: order.accepted dan keyin trip.completed kelmasa ham haydovchi abadiy band qolmasin.
	BusyTTL time.Duration `envconfig:"BUSY_TTL" default:"24h"`

	// F-11: ikki joylashuv orasidagi tezlik shundan oshsa (va masofa MinJumpM dan katta bo'lsa) signal.
	MaxSpeedKMH float64 `envconfig:"MAX_SPEED_KMH" default:"300"`
	MinJumpM    float64 `envconfig:"MIN_JUMP_M" default:"200"` // GPS shovqini signal bermasligi uchun

	DefaultRadiusM float64 `envconfig:"DEFAULT_RADIUS_M" default:"50000"`
	MaxRadiusM     float64 `envconfig:"MAX_RADIUS_M" default:"200000"`
	// Geo-so'rovdan keyin filtrlanadigan nomzodlar soni (filtr Redis'dan keyin bajariladi).
	MaxCandidates int `envconfig:"MAX_CANDIDATES" default:"500"`

	// order_id berilgan so'rovda taklif muddati (expires_at berilmasa).
	OfferTTL time.Duration `envconfig:"OFFER_TTL" default:"2m"`

	// Marshrut (mock): masofa = Haversine × RoadFactor — Pricing bilan bir xil (shared/geo).
	RoadFactor  float64 `envconfig:"ROAD_FACTOR" default:"1.3"`
	AvgSpeedKMH float64 `envconfig:"AVG_SPEED_KMH" default:"30"`
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
	for name, v := range map[string]string{"REDIS_URL": c.RedisURL, "NATS_URL": c.NATSURL, "JWT_PUBLIC_KEY_PATH": c.JWTPublicKeyPath} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s bo'sh bo'lmasligi kerak", name))
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
	for name, v := range map[string]time.Duration{"LOCATION_TTL": c.LocationTTL, "BUSY_TTL": c.BusyTTL, "OFFER_TTL": c.OfferTTL} {
		if v <= 0 {
			errs = append(errs, fmt.Errorf("%s musbat bo'lishi kerak", name))
		}
	}
	if c.MaxSpeedKMH <= 0 || c.MinJumpM < 0 {
		errs = append(errs, errors.New("MAX_SPEED_KMH musbat, MIN_JUMP_M manfiy bo'lmasligi kerak"))
	}
	if c.DefaultRadiusM <= 0 || c.MaxRadiusM < c.DefaultRadiusM {
		errs = append(errs, errors.New("DEFAULT_RADIUS_M musbat va MAX_RADIUS_M dan oshmasligi kerak"))
	}
	if c.MaxCandidates < 1 || c.MaxCandidates > 5000 {
		errs = append(errs, errors.New("MAX_CANDIDATES 1..5000 oralig'ida bo'lishi kerak"))
	}
	return errors.Join(errs...)
}
