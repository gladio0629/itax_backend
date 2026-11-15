package calc

import (
	"math"

	"github.com/izgo/pricing/internal/tariff"
)

// Line — narx tarkibining bir qatori. Key — klient i18n kaliti (masalan `quote.seatFare`), Params — matnga qo'yiladigan
// qiymatlar, Amount — tiyinda. Qatorlar yig'indisi DOIM Result.Price ga teng (klient shuni ko'rsatadi, server shuni oladi).
type Line struct {
	Key    string         `json:"key"`
	Params map[string]any `json:"params,omitempty"`
	Amount int64          `json:"amount"`
}

// UI sinf identifikatorlari (klient i18n: quote.class.komfort, ...). economy uchun alohida qator yo'q.
var classLineKey = map[string]string{"comfort": "quote.class.komfort", "business": "quote.class.biznes", "minivan": "quote.class.miniven"}

// Lines — narxni klient ko'rsatadigan qatorlarga bo'ladi. Oxirgi qator qoldiqni oladi, shuning uchun yaxlitlash va
// minimal narx qatorlar yig'indisini buzmaydi.
//
//	passenger + seats:     quote.seatFare {count}
//	passenger + car_class: quote.carFare (economy narxi) + quote.class.<ui> {pct} (farq) — economy'da faqat carFare
//	delivery:              quote.cargoBase (bazaviy narx) + quote.cargoDistance {km} (qolgani)
func Lines(t tariff.Tariff, in Input, price int64) []Line {
	switch in.Type {
	case Passenger:
		if in.Seats != nil {
			return []Line{{Key: "quote.seatFare", Params: map[string]any{"count": *in.Seats}, Amount: price}}
		}
		if in.CarClass != nil {
			key, ok := classLineKey[*in.CarClass]
			econ, eok := t.CarClassMultipliers["economy"]
			cls := t.CarClassMultipliers[*in.CarClass]
			if ok && eok && econ > 0 && cls > econ {
				e := "economy"
				if base, err := Price(t, Input{Type: Passenger, CarClass: &e, DistanceM: in.DistanceM}); err == nil && base.Price < price {
					pct := int(math.Round(float64(cls-econ) * 100 / float64(econ)))
					return []Line{
						{Key: "quote.carFare", Amount: base.Price},
						{Key: key, Params: map[string]any{"pct": pct}, Amount: price - base.Price},
					}
				}
			}
			return []Line{{Key: "quote.carFare", Amount: price}}
		}
	case Delivery:
		b := t.BaseFare
		if b > price {
			b = price
		}
		km := math.Round(float64(in.DistanceM)/100) / 10
		return []Line{
			{Key: "quote.cargoBase", Amount: b},
			{Key: "quote.cargoDistance", Params: map[string]any{"km": km}, Amount: price - b},
		}
	}
	return []Line{{Key: "quote.carFare", Amount: price}}
}
