// Package store — Redis: haydovchi joylashuvi (GEO), holati, profili va safar bog'lanishi. PostgreSQL yo'q.
//
// Kalitlar:
//
//	drivers:geo              GEO (zset): a'zo = haydovchi UUID
//	driver:{id}:loc          "lat,lng,unix_ms"  EX LocationTTL  — jonlilik belgisi va oldingi nuqta (tezlik tekshiruvi)
//	driver:{id}:status       available | busy | offline          (kalit yo'q = available)
//	driver:{id}:profile      HASH state, seats, car_class, vehicle_type
//	driver:{id}:trip         joriy safar ID si (busy bo'lganda)
//	matching:trip-done:{id}  tugagan safar belgisi (kech/takror kelgan order.accepted ni e'tiborsiz qoldirish uchun)
//	matching:gps-anomalies   STREAM (F-11 signallari)
package store

import (
	"strings"

	"github.com/google/uuid"
)

type Status string

const (
	Available Status = "available"
	Busy      Status = "busy"
	Offline   Status = "offline"
)

func (s Status) Valid() bool { return s == Available || s == Busy || s == Offline }

// DriverStates — Driver servisining holatlari (openapi DriverState). Faqat "active" haydovchilar taklif oladi.
var DriverStates = []string{"draft", "pending_review", "active", "rejected", "blocked"}

const StateActive = "active"

type Profile struct {
	State       string
	Seats       int    // yo'lovchi o'rindiqlari soni (0 — yo'lovchi tashimaydi)
	CarClass    string // butun mashina buyurtmalari uchun (ixtiyoriy)
	VehicleType string // yuk tashish (delivery) uchun (ixtiyoriy)
}

type OrderKind string

const (
	KindPassenger OrderKind = "passenger"
	KindDelivery  OrderKind = "delivery"
)

// Query — radius qidiruvi.
type Query struct {
	Lat, Lng      float64
	RadiusM       float64
	Kind          OrderKind
	Seats         *int
	CarClass      *string
	VehicleType   *string
	Limit         int
	MaxCandidates int
}

type Candidate struct {
	DriverID  uuid.UUID
	Lat, Lng  float64
	DistanceM float64
}

func driverKey(id uuid.UUID, suffix string) string {
	return "driver:" + id.String() + ":" + suffix
}

// Eligible — haydovchi shu buyurtmaga mosmi (sof funksiya).
//
//	holati active · jonli joylashuv · status available (faol safari yo'q) ·
//	passenger: o'rindiqlar >= so'ralgan, yoki car_class aynan teng · delivery: vehicle_type aynan teng
func Eligible(q Query, p Profile, st Status, live bool) bool {
	if !live || p.State != StateActive || st != Available {
		return false
	}
	switch q.Kind {
	case KindPassenger:
		switch {
		case q.Seats != nil:
			return p.Seats >= *q.Seats && p.Seats > 0
		case q.CarClass != nil:
			return p.CarClass != "" && strings.EqualFold(p.CarClass, *q.CarClass)
		}
	case KindDelivery:
		return q.VehicleType != nil && p.VehicleType != "" && p.VehicleType == *q.VehicleType
	}
	return false
}
