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
	Port string `envconfig:"PORT" default:"8014"`

	DBHost     string `envconfig:"DB_HOST" default:"postgres"`
	DBPort     int    `envconfig:"DB_PORT" default:"5432"`
	DBUser     string `envconfig:"DB_USER" default:"pricing_svc"`
	DBPassword string `envconfig:"DB_PASSWORD"`
	DBName     string `envconfig:"DB_NAME" default:"pricing_db"`
	DBSSLMode  string `envconfig:"DB_SSLMODE" default:"disable"`

	RedisURL string `envconfig:"REDIS_URL" required:"true"`

	// Ichki servislararo chaqiruv uchun umumiy sir (vergul bilan ajratilgan bir nechta qiymat — almashtirish davrida
	// eski va yangi birga ishlashi uchun). Har biri kamida 16 belgi.
	InternalTokens []string `envconfig:"INTERNAL_TOKENS" required:"true"`

	TariffCacheTTL time.Duration `envconfig:"TARIFF_CACHE_TTL" default:"5m"`

	// Mapbox kelguncha: masofa = Haversine × RoadFactor, davomiylik = masofa / AvgSpeedKMH.
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
	for name, v := range map[string]string{"DB_PASSWORD": c.DBPassword, "REDIS_URL": c.RedisURL} {
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
	if c.TariffCacheTTL <= 0 {
		errs = append(errs, errors.New("TARIFF_CACHE_TTL musbat bo'lishi kerak"))
	}
	return errors.Join(errs...)
}
