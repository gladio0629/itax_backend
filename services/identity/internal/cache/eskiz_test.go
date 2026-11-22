package cache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestEskizTokenRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := NewEskizToken(rdb)
	ctx := context.Background()

	if got, err := s.Get(ctx); err != nil || got != "" {
		t.Fatalf("bo'sh kesh: %q, %v", got, err)
	}
	if err := s.Set(ctx, "tok-1", 29*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx); got != "tok-1" {
		t.Fatalf("Get = %q", got)
	}
	if ttl := mr.TTL(EskizTokenKey); ttl != 29*24*time.Hour {
		t.Fatalf("TTL = %v, want 29 kun", ttl)
	}
	mr.FastForward(29*24*time.Hour + time.Second)
	if got, _ := s.Get(ctx); got != "" {
		t.Fatalf("muddati o'tgan token qaytdi: %q", got)
	}
	_ = s.Set(ctx, "tok-2", time.Hour)
	if err := s.Del(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx); got != "" {
		t.Fatalf("Del dan keyin: %q", got)
	}
}
