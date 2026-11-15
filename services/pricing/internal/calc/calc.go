// Package calc — narx formulasi (sof funksiya: tarif + masofa -> narx). Hamma hisob butun sonlarda,
// oraliq natijalar math/big da (masofa × tarif × koeffitsient int64 dan oshib ketishi mumkin).
//
//	Passenger (o'rindiq):  baseFare + distance_km × perKmRate × seats × seatMultiplier
//	Passenger (butun mashina): baseFare + distance_km × perKmRate × carClassMultiplier
//	Delivery:              baseFare + distance_km × perKmRate × vehicleMultiplier
//	so'ng: × (1 + fuelSurcharge) → max(minFare) → yaxlitlash. Barcha bo'linishlar "yarmi yuqoriga".
package calc

import (
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/izgo/pricing/internal/tariff"
)

type OrderType string

const (
	Passenger OrderType = "passenger"
	Delivery  OrderType = "delivery"
)

// Input — narx so'rovining hisob uchun kerakli qismi.
type Input struct {
	Type        OrderType
	Seats       *int
	CarClass    *string
	VehicleType *string
	DistanceM   int
}

type Result struct {
	Price      int64 // tiyin
	Commission int64 // tiyin
}

var ErrInvalidInput = errors.New("calc: invalid input")

// factorPermille — tur bo'yicha umumiy omil (promilleda).
func factorPermille(t tariff.Tariff, in Input) (int64, error) {
	switch in.Type {
	case Passenger:
		switch {
		case in.VehicleType != nil:
			return 0, fmt.Errorf("%w: vehicle_type passenger uchun emas", ErrInvalidInput)
		case in.Seats != nil && in.CarClass != nil:
			return 0, fmt.Errorf("%w: seats va car_class birga", ErrInvalidInput)
		case in.Seats != nil:
			if *in.Seats < 1 || *in.Seats > 4 {
				return 0, fmt.Errorf("%w: seats 1..4", ErrInvalidInput)
			}
			return int64(*in.Seats) * t.SeatMultiplier, nil
		case in.CarClass != nil:
			m, ok := t.CarClassMultipliers[*in.CarClass]
			if !ok {
				return 0, fmt.Errorf("%w: car_class %q", ErrInvalidInput, *in.CarClass)
			}
			return m, nil
		}
		return 0, fmt.Errorf("%w: seats yoki car_class kerak", ErrInvalidInput)
	case Delivery:
		if in.Seats != nil || in.CarClass != nil {
			return 0, fmt.Errorf("%w: seats/car_class delivery uchun emas", ErrInvalidInput)
		}
		if in.VehicleType == nil {
			return 0, fmt.Errorf("%w: vehicle_type kerak", ErrInvalidInput)
		}
		m, ok := t.VehicleMultipliers[*in.VehicleType]
		if !ok {
			return 0, fmt.Errorf("%w: vehicle_type %q", ErrInvalidInput, *in.VehicleType)
		}
		return m, nil
	}
	return 0, fmt.Errorf("%w: type %q", ErrInvalidInput, in.Type)
}

// divRound — a/b, yarmi yuqoriga (a >= 0, b > 0).
func divRound(a, b *big.Int) *big.Int {
	half := new(big.Int).Rsh(b, 1)
	q := new(big.Int).Add(a, half)
	return q.Quo(q, b)
}

func Price(t tariff.Tariff, in Input) (Result, error) {
	if in.DistanceM < 0 {
		return Result{}, fmt.Errorf("%w: distance < 0", ErrInvalidInput)
	}
	factor, err := factorPermille(t, in)
	if err != nil {
		return Result{}, err
	}

	// km qismi: metr × (tiyin/km) × omil‰ / (1000 m/km × 1000‰)
	km := new(big.Int).Mul(big.NewInt(int64(in.DistanceM)), big.NewInt(t.PerKmRate))
	km.Mul(km, big.NewInt(factor))
	km = divRound(km, big.NewInt(1_000_000))

	total := km.Add(km, big.NewInt(t.BaseFare))

	// yoqilg'i ustamasi
	if t.FuelSurcharge > 0 {
		total.Mul(total, big.NewInt(1000+t.FuelSurcharge))
		total = divRound(total, big.NewInt(1000))
	}
	// minimal narx
	if min := big.NewInt(t.MinFare); total.Cmp(min) < 0 {
		total = min
	}
	// yaxlitlash (eng yaqin qadamga, yarmi yuqoriga)
	if t.RoundingStep > 1 {
		step := big.NewInt(t.RoundingStep)
		total = divRound(total, step)
		total.Mul(total, step)
	}

	if !total.IsInt64() || total.Int64() > math.MaxInt64/2 {
		return Result{}, fmt.Errorf("%w: narx juda katta", ErrInvalidInput)
	}
	price := total.Int64()
	comm := divRound(new(big.Int).Mul(total, big.NewInt(t.CommissionBPS)), big.NewInt(10_000))
	return Result{Price: price, Commission: comm.Int64()}, nil
}
