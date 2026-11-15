// Package external — boshqa servislarga sinxron chaqiruvlar uchun interfeyslar, real HTTP klientlar (http.go) va mock'lar (mocks.go).
// Qaysi biri ishlashi New() da *_URL bo'yicha tanlanadi.
package external

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/domain"
)

// ---- Pricing (sinxron)

type QuoteRequest struct {
	Type        domain.OrderType
	Seats       *int
	CarClass    *domain.CarClass
	VehicleType *domain.VehicleType
	From, To    domain.Point
	ScheduledAt *time.Time
}

type Quote struct {
	Price      int64 // tiyin
	Commission int64 // tiyin
	DistanceM  int
	DurationS  int
	// Lines — narx tarkibi (yig'indisi = Price). Bo'sh bo'lsa chaqiruvchi domain.DefaultLines ishlatadi.
	Lines []domain.PriceLine
}

type FinalizeRequest struct {
	OrderID     uuid.UUID
	Quoted      Quote
	StartedAt   time.Time
	CompletedAt time.Time
}

type Final struct {
	Price      int64
	Commission int64
	DistanceM  int
}

type Pricer interface {
	// Quote — buyurtma narxi. Klient narx yubormaydi: faqat shu javob ishlatiladi.
	Quote(ctx context.Context, r QuoteRequest) (Quote, error)
	// Finalize — safar tugagach yakuniy narx, masofa va komissiya.
	Finalize(ctx context.Context, r FinalizeRequest) (Final, error)
}

// ---- Wallet (sinxron, F-13)

type WalletStatus struct {
	LimitReached bool
	Limit        int64 // tiyin (qarz chegarasi)
}

type Wallet interface {
	// CheckDriver — haydovchi qarz chegarasidami (shunda yangi buyurtma qabul qila olmaydi).
	CheckDriver(ctx context.Context, driverID uuid.UUID) (WalletStatus, error)
}

// ---- Matching

type Candidate struct {
	DriverID         uuid.UUID
	PickupETASeconds int
}

type Matcher interface {
	// FindDrivers — buyurtma uchun mos haydovchilar (taklif shularga yaratiladi).
	FindDrivers(ctx context.Context, o domain.Order) ([]Candidate, error)
}
