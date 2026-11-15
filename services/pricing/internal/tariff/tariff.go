// Package tariff — tarif modeli, tekshiruvi, bazadan o'qish va Redis keshi.
//
// Tarifni Backoffice o'zgartiradi (ikki kishi tasdig'i bilan), Pricing faqat O'QIYDI: bu paket hech qachon
// tarifni yozmaydi. Amaldagi tarif — `active = true` bo'lgan yagona qator.
package tariff

import (
	"errors"
	"fmt"
)

// Barcha ko'paytirgichlar promilleda: 1000 = 1.0×. Pul — tiyinda (1 so'm = 100 tiyin).
//
// OCHIQ (hujjatda hali tasdiqlanmagan): formula va koeffitsientlar taklif; shu sababli hammasi bazada.
type Tariff struct {
	ID      string `json:"id"`
	Version int    `json:"version"`

	BaseFare  int64 `json:"base_fare"`   // tiyin
	PerKmRate int64 `json:"per_km_rate"` // tiyin / km

	// SeatMultiplier — o'rindiq soniga ko'paytirgich: omil = seats × SeatMultiplier/1000 (default 1000 → seats).
	SeatMultiplier int64 `json:"seat_multiplier"`
	// CarClassMultipliers — butun mashina (car_class) omili, masalan business: 6400 → 6.4×.
	CarClassMultipliers map[string]int64 `json:"car_class_multipliers"`
	// VehicleMultipliers — yuk mashinasi (delivery) omili: spark 1000, damas 1300, ...
	VehicleMultipliers map[string]int64 `json:"vehicle_multipliers"`

	// FuelSurcharge — yoqilg'i ustamasi, promilleda: yig'indiga qo'shiladi (100 → +10%).
	FuelSurcharge int64 `json:"fuel_surcharge"`

	// Qo'shimchalar (formulaga zarur, lekin ro'yxatda yo'q edi): minimal narx, yaxlitlash qadami, komissiya.
	MinFare       int64 `json:"min_fare"`       // tiyin
	RoundingStep  int64 `json:"rounding_step"`  // tiyin; <=1 — yaxlitlanmaydi
	CommissionBPS int64 `json:"commission_bps"` // 1000 = 10%
}

var (
	PassengerClasses = []string{"economy", "comfort", "business", "minivan"}
	VehicleTypes     = []string{"spark", "damas", "labo", "isuzu", "isuzu2", "fura"}
)

const (
	maxMoney      = 1_000_000_000_000 // 10 mlrd so'm — mantiqiy yuqori chegara
	maxMultiplier = 100_000           // 100×
)

var ErrNoActiveTariff = errors.New("tariff: no active tariff")

// Validate — buzuq tarif (qo'lda tahrirlangan DB qatori, eskirgan kesh) hisoblashga tushmasligi uchun.
func (t Tariff) Validate() error {
	var errs []error
	money := func(name string, v int64) {
		if v < 0 || v > maxMoney {
			errs = append(errs, fmt.Errorf("%s: 0..%d oralig'ida bo'lishi kerak", name, int64(maxMoney)))
		}
	}
	mult := func(name string, v int64) {
		if v <= 0 || v > maxMultiplier {
			errs = append(errs, fmt.Errorf("%s: 1..%d oralig'ida bo'lishi kerak", name, maxMultiplier))
		}
	}
	money("base_fare", t.BaseFare)
	money("per_km_rate", t.PerKmRate)
	money("min_fare", t.MinFare)
	money("rounding_step", t.RoundingStep)
	mult("seat_multiplier", t.SeatMultiplier)
	if t.FuelSurcharge < 0 || t.FuelSurcharge > 10_000 {
		errs = append(errs, errors.New("fuel_surcharge: 0..10000 promille"))
	}
	if t.CommissionBPS < 0 || t.CommissionBPS > 10_000 {
		errs = append(errs, errors.New("commission_bps: 0..10000"))
	}
	for _, k := range PassengerClasses {
		v, ok := t.CarClassMultipliers[k]
		if !ok {
			errs = append(errs, fmt.Errorf("car_class_multipliers.%s yo'q", k))
			continue
		}
		mult("car_class_multipliers."+k, v)
	}
	for _, k := range VehicleTypes {
		v, ok := t.VehicleMultipliers[k]
		if !ok {
			errs = append(errs, fmt.Errorf("vehicle_multipliers.%s yo'q", k))
			continue
		}
		mult("vehicle_multipliers."+k, v)
	}
	return errors.Join(errs...)
}
