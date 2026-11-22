package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// EskizTokenKey — Eskiz.uz Bearer tokeni (notify.TokenStore). Bir nechta identity nusxasi bitta tokenni ishlatadi.
const EskizTokenKey = "sms:eskiz:token"

// EskizToken — notify.TokenStore ning Redis amalga oshirilishi.
type EskizToken struct{ rdb redis.Cmdable }

func NewEskizToken(rdb redis.Cmdable) *EskizToken { return &EskizToken{rdb: rdb} }

// Get — token yo'q bo'lsa "" va nil.
func (e *EskizToken) Get(ctx context.Context) (string, error) {
	t, err := e.rdb.Get(ctx, EskizTokenKey).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("eskiz token: %w", err)
	}
	return t, nil
}

func (e *EskizToken) Set(ctx context.Context, token string, ttl time.Duration) error {
	if err := e.rdb.Set(ctx, EskizTokenKey, token, ttl).Err(); err != nil {
		return fmt.Errorf("eskiz token: %w", err)
	}
	return nil
}

func (e *EskizToken) Del(ctx context.Context) error {
	if err := e.rdb.Del(ctx, EskizTokenKey).Err(); err != nil {
		return fmt.Errorf("eskiz token: %w", err)
	}
	return nil
}
