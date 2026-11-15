// Package guard — Redis asosidagi qulflar va hisoblagichlar: qabul qulfi (SETNX) va faol buyurtmalar hisobi.
package guard

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Guard struct{ rdb redis.Cmdable }

func New(rdb redis.Cmdable) *Guard { return &Guard{rdb: rdb} }

func lockKey(orderID uuid.UUID) string { return "order:accept-lock:" + orderID.String() }

// TryAcceptLock — "birinchi qabul qilgan oladi": SETNX. true — qulf shu haydovchida.
func (g *Guard) TryAcceptLock(ctx context.Context, orderID, driverID uuid.UUID, ttl time.Duration) (bool, error) {
	ok, err := g.rdb.SetNX(ctx, lockKey(orderID), driverID.String(), ttl).Result()
	if err != nil {
		return false, fmt.Errorf("guard: accept lock: %w", err)
	}
	return ok, nil
}

var releaseLock = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0
`)

// ReleaseAcceptLock — qulfni faqat egasi olib tashlay oladi (compare-and-delete).
func (g *Guard) ReleaseAcceptLock(ctx context.Context, orderID, driverID uuid.UUID) error {
	if err := releaseLock.Run(ctx, g.rdb, []string{lockKey(orderID)}, driverID.String()).Err(); err != nil && err != redis.Nil {
		return fmt.Errorf("guard: release lock: %w", err)
	}
	return nil
}

// ---- faol buyurtmalar hisobi (F-24)

type Kind string

const (
	Passenger Kind = "p"
	Driver    Kind = "d"
)

func activeKey(k Kind, id uuid.UUID) string { return "active:" + string(k) + ":" + id.String() }

// Hisoblagich muddati: sızıb qolsa ham o'zi tozalanadi; asosiy tuzatish — DB bilan qayta sanash (Heal).
const activeTTL = 7 * 24 * time.Hour

// Natija: 1 — joy band qilindi; 0 — limitga yetgan; -1 — hisoblagich yo'q (Redis yo'qolgan yoki birinchi marta):
// chaqiruvchi uni bazadagi haqiqiy son bilan tiklashi kerak.
var reserveScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return -1 end
local c = tonumber(redis.call('GET', KEYS[1]))
if c >= tonumber(ARGV[1]) then return 0 end
redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], ARGV[2])
return 1
`)

type Reservation int

const (
	Reserved Reservation = 1
	Full     Reservation = 0
	Unknown  Reservation = -1
)

// Reserve — hisoblagichni atomik oshiradi.
func (g *Guard) Reserve(ctx context.Context, k Kind, id uuid.UUID, limit int) (Reservation, error) {
	n, err := reserveScript.Run(ctx, g.rdb, []string{activeKey(k, id)}, limit, int(activeTTL.Seconds())).Int()
	if err != nil {
		return Full, fmt.Errorf("guard: reserve: %w", err)
	}
	return Reservation(n), nil
}

var releaseScript = redis.NewScript(`
local c = tonumber(redis.call('GET', KEYS[1]) or '0')
if c > 0 then redis.call('DECR', KEYS[1]) end
return 1
`)

// Release — hisoblagichni kamaytiradi (0 dan pastga tushmaydi).
func (g *Guard) Release(ctx context.Context, k Kind, id uuid.UUID) error {
	if err := releaseScript.Run(ctx, g.rdb, []string{activeKey(k, id)}).Err(); err != nil {
		return fmt.Errorf("guard: release: %w", err)
	}
	return nil
}

// Heal — hisoblagichni bazadagi haqiqiy son bilan tenglashtiradi (Redis yo'qolgan/siljigan holat uchun).
func (g *Guard) Heal(ctx context.Context, k Kind, id uuid.UUID, actual int) error {
	if err := g.rdb.Set(ctx, activeKey(k, id), actual, activeTTL).Err(); err != nil {
		return fmt.Errorf("guard: heal: %w", err)
	}
	return nil
}

// Count — joriy qiymat (asosan testlar uchun).
func (g *Guard) Count(ctx context.Context, k Kind, id uuid.UUID) (int, error) {
	n, err := g.rdb.Get(ctx, activeKey(k, id)).Int()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("guard: count: %w", err)
	}
	return n, nil
}
