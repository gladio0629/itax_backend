package tariff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Source — amaldagi tarifning manbasi.
type Source interface {
	Active(ctx context.Context) (Tariff, error)
}

// Store — pricing_db dan o'qiydi (yozmaydi).
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Active(ctx context.Context) (Tariff, error) {
	var (
		t         Tariff
		cars, veh []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, version, base_fare, per_km_rate, seat_multiplier, car_class_multipliers, vehicle_multipliers,
		       fuel_surcharge, min_fare, rounding_step, commission_bps
		FROM tariffs WHERE active`).
		Scan(&t.ID, &t.Version, &t.BaseFare, &t.PerKmRate, &t.SeatMultiplier, &cars, &veh,
			&t.FuelSurcharge, &t.MinFare, &t.RoundingStep, &t.CommissionBPS)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tariff{}, ErrNoActiveTariff
	}
	if err != nil {
		return Tariff{}, fmt.Errorf("tariff: o'qish: %w", err)
	}
	if err := json.Unmarshal(cars, &t.CarClassMultipliers); err != nil {
		return Tariff{}, fmt.Errorf("tariff: car_class_multipliers: %w", err)
	}
	if err := json.Unmarshal(veh, &t.VehicleMultipliers); err != nil {
		return Tariff{}, fmt.Errorf("tariff: vehicle_multipliers: %w", err)
	}
	if err := t.Validate(); err != nil {
		return Tariff{}, fmt.Errorf("tariff: v%d yaroqsiz: %w", t.Version, err)
	}
	return t, nil
}

const cacheKey = "pricing:tariff:active"

// Cache — amaldagi tarifni Redis'da saqlaydi (TTL default 5 daqiqa). Redis ishlamasa to'g'ridan-to'g'ri
// bazadan o'qiladi: kesh faqat tezlashtirish, narx hisoblashni to'xtatmaydi.
//
// Tarif o'zgarganda keshni Invalidate() tozalaydi (keyinroq NATS hodisasidan chaqiriladi); hodisa bo'lmasa
// yangi tarif eng ko'pi bilan TTL dan keyin kuchga kiradi.
type Cache struct {
	rdb redis.Cmdable
	src Source
	ttl time.Duration
	log *slog.Logger
}

func NewCache(rdb redis.Cmdable, src Source, ttl time.Duration, log *slog.Logger) *Cache {
	return &Cache{rdb: rdb, src: src, ttl: ttl, log: log}
}

func (c *Cache) Active(ctx context.Context) (Tariff, error) {
	raw, err := c.rdb.Get(ctx, cacheKey).Bytes()
	switch {
	case err == nil:
		var t Tariff
		if jerr := json.Unmarshal(raw, &t); jerr == nil && t.Validate() == nil {
			return t, nil
		}
		c.log.WarnContext(ctx, "keshdagi tarif buzuq: bazadan qayta o'qiladi")
	case !errors.Is(err, redis.Nil):
		c.log.WarnContext(ctx, "tarif keshi ishlamadi: bazadan o'qiladi", "err", err)
	}

	t, err := c.src.Active(ctx)
	if err != nil {
		return Tariff{}, err
	}
	if body, merr := json.Marshal(t); merr == nil {
		if serr := c.rdb.Set(ctx, cacheKey, body, c.ttl).Err(); serr != nil {
			c.log.WarnContext(ctx, "tarifni keshga yozib bo'lmadi", "err", serr)
		}
	}
	return t, nil
}

// Invalidate — keshni tozalaydi (tarif o'zgarganda).
func (c *Cache) Invalidate(ctx context.Context) error {
	if err := c.rdb.Del(ctx, cacheKey).Err(); err != nil {
		return fmt.Errorf("tariff: kesh tozalash: %w", err)
	}
	return nil
}
