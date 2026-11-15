package calc

import (
	"testing"

	"github.com/izgo/pricing/internal/tariff"
)

type tariffT = tariff.Tariff

func sum(ls []Line) int64 {
	var s int64
	for _, l := range ls {
		s += l.Amount
	}
	return s
}

// Qatorlar yig'indisi har qanday tarif sozlamasida (yoqilg'i ustamasi, minimal narx, yaxlitlash) narxga teng.
func TestLinesSumEqualsPrice(t *testing.T) {
	tariffs := map[string]func() tariffT{
		"plain":    func() tariffT { return base() },
		"rounding": func() tariffT { tr := base(); tr.RoundingStep = 50_000; tr.FuelSurcharge = 75; return tr },
		"min_fare": func() tariffT { tr := base(); tr.MinFare = 9_000_000; tr.RoundingStep = 10_000; return tr },
	}
	inputs := []Input{
		{Type: Passenger, Seats: ip(1)}, {Type: Passenger, Seats: ip(3)},
		{Type: Passenger, CarClass: sp("economy")}, {Type: Passenger, CarClass: sp("comfort")},
		{Type: Passenger, CarClass: sp("business")}, {Type: Passenger, CarClass: sp("minivan")},
		{Type: Delivery, VehicleType: sp("spark")}, {Type: Delivery, VehicleType: sp("fura")},
	}
	for name, mk := range tariffs {
		for _, d := range []int{0, 1_234, 10_000, 333_333} {
			for _, in := range inputs {
				in.DistanceM = d
				tr := mk()
				r := price(t, tr, in)
				ls := Lines(tr, in, r.Price)
				if len(ls) == 0 || sum(ls) != r.Price {
					t.Fatalf("%s d=%d %+v: lines=%+v sum=%d price=%d", name, d, in, ls, sum(ls), r.Price)
				}
				for _, l := range ls {
					if l.Amount < 0 {
						t.Fatalf("%s %+v: manfiy qator %+v", name, in, l)
					}
				}
			}
		}
	}
}

func TestLinesKeys(t *testing.T) {
	tr := base()
	in := Input{Type: Passenger, CarClass: sp("comfort"), DistanceM: 10_000}
	r := price(t, tr, in)
	ls := Lines(tr, in, r.Price)
	if len(ls) != 2 || ls[0].Key != "quote.carFare" || ls[1].Key != "quote.class.komfort" || ls[1].Params["pct"] != 25 {
		t.Fatalf("comfort lines: %+v", ls)
	}
	seats := Input{Type: Passenger, Seats: ip(2), DistanceM: 10_000}
	if ls := Lines(tr, seats, price(t, tr, seats).Price); len(ls) != 1 || ls[0].Key != "quote.seatFare" || ls[0].Params["count"] != 2 {
		t.Fatalf("seat lines: %+v", ls)
	}
	cargo := Input{Type: Delivery, VehicleType: sp("damas"), DistanceM: 12_345}
	ls = Lines(tr, cargo, price(t, tr, cargo).Price)
	if len(ls) != 2 || ls[0].Key != "quote.cargoBase" || ls[0].Amount != tr.BaseFare || ls[1].Params["km"] != 12.3 {
		t.Fatalf("cargo lines: %+v", ls)
	}
}
