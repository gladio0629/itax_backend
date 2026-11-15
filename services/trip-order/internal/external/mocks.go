package external

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------- MockPricer

// MockPricer — haversine masofa asosida deterministik narx. Real Pricing servisi o'rniga.
type MockPricer struct{}

const (
	baseFare      = 400_000 // 4 000 so'm, tiyinda
	perKm         = 120_000 // 1 200 so'm/km
	minFare       = 700_000
	roundTo       = 10_000 // 100 so'mga yaxlitlash
	commissionBPS = 1000   // 10%
	roadFactorPct = 130    // to'g'ri chiziqqa nisbatan yo'l uzunligi
	avgSpeedMps   = 8      // ~29 km/soat
)

// seat ulushi (bps): 1 o'rindiq butun mashina narxining 35% i va h.k.
var seatBPS = map[int]int64{1: 3500, 2: 6500, 3: 9000, 4: 10000}

var classBPS = map[domain.CarClass]int64{
	domain.Economy: 10000, domain.Comfort: 12500, domain.Business: 16000, domain.Minivan: 15000,
}

var vehicleBPS = map[domain.VehicleType]int64{
	domain.Spark: 10000, domain.Damas: 13000, domain.Labo: 16000,
	domain.Isuzu: 22000, domain.Isuzu2: 26000, domain.Fura: 40000,
}

func (MockPricer) Quote(_ context.Context, r QuoteRequest) (Quote, error) {
	dist := roadDistanceM(r.From, r.To)
	full := baseFare + int64(math.Round(float64(dist)/1000*perKm))

	var bps int64
	switch r.Type {
	case domain.TypePassenger:
		switch {
		case r.Seats != nil:
			b, ok := seatBPS[*r.Seats]
			if !ok {
				return Quote{}, fmt.Errorf("pricing: seats %d", *r.Seats)
			}
			bps = b
		case r.CarClass != nil:
			b, ok := classBPS[*r.CarClass]
			if !ok {
				return Quote{}, fmt.Errorf("pricing: car_class %q", *r.CarClass)
			}
			bps = b
		default:
			return Quote{}, fmt.Errorf("pricing: seats yoki car_class kerak")
		}
	case domain.TypeDelivery:
		if r.VehicleType == nil {
			return Quote{}, fmt.Errorf("pricing: vehicle_type kerak")
		}
		b, ok := vehicleBPS[*r.VehicleType]
		if !ok {
			return Quote{}, fmt.Errorf("pricing: vehicle_type %q", *r.VehicleType)
		}
		bps = b
	default:
		return Quote{}, fmt.Errorf("pricing: type %q", r.Type)
	}

	price := roundPrice(full * bps / 10000)
	return Quote{
		Price:      price,
		Commission: price * commissionBPS / 10000,
		DistanceM:  dist,
		DurationS:  dist / avgSpeedMps,
	}, nil
}

// Finalize (mock) — kelishilgan narxni o'zgartirmaydi.
func (MockPricer) Finalize(_ context.Context, r FinalizeRequest) (Final, error) {
	return Final{Price: r.Quoted.Price, Commission: r.Quoted.Commission, DistanceM: r.Quoted.DistanceM}, nil
}

func roundPrice(p int64) int64 {
	if p < minFare {
		p = minFare
	}
	return (p + roundTo/2) / roundTo * roundTo
}

func roadDistanceM(a, b domain.Point) int {
	const earthR = 6371000.0
	rad := math.Pi / 180
	dLat := (b.Lat - a.Lat) * rad
	dLng := (b.Lng - a.Lng) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	d := 2 * earthR * math.Asin(math.Min(1, math.Sqrt(h)))
	return int(math.Round(d * roadFactorPct / 100))
}

// ---------------------------------------------------------------- MockWallet

// AllowAllWallet — hozir doim ruxsat beradi (F-13 mock).
type AllowAllWallet struct{}

func (AllowAllWallet) CheckDriver(context.Context, uuid.UUID) (WalletStatus, error) {
	return WalletStatus{}, nil
}

// ---------------------------------------------------------------- MockMatcher

// MatchingMockKey — Redis SET: mock matching shu to'plamdagi haydovchilarga taklif yaratadi.
// Lokal ishlab chiqishda:  redis-cli SADD matching:mock:drivers <driver-uuid>
const MatchingMockKey = "matching:mock:drivers"

const mockPickupETA = 300

type RedisMatcher struct{ RDB redis.Cmdable }

func (m RedisMatcher) FindDrivers(ctx context.Context, _ domain.Order) ([]Candidate, error) {
	members, err := m.RDB.SMembers(ctx, MatchingMockKey).Result()
	if err != nil {
		return nil, fmt.Errorf("matching mock: %w", err)
	}
	out := make([]Candidate, 0, len(members))
	for _, s := range members {
		id, err := uuid.Parse(s)
		if err != nil {
			continue // noto'g'ri yozuv e'tiborsiz
		}
		out = append(out, Candidate{DriverID: id, PickupETASeconds: mockPickupETA})
		if len(out) == 20 {
			break
		}
	}
	return out, nil
}
