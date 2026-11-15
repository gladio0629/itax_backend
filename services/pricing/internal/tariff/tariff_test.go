package tariff

import "testing"

func valid() Tariff {
	return Tariff{
		BaseFare: 400_000, PerKmRate: 120_000, SeatMultiplier: 1000,
		CarClassMultipliers: map[string]int64{"economy": 4000, "comfort": 5000, "business": 6400, "minivan": 6000},
		VehicleMultipliers:  map[string]int64{"spark": 1000, "damas": 1300, "labo": 1500, "isuzu": 2000, "isuzu2": 2500, "fura": 3000},
		MinFare:             700_000, RoundingStep: 10_000, CommissionBPS: 1000,
	}
}

func TestValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	mut := map[string]func(*Tariff){
		"manfiy base_fare":   func(t *Tariff) { t.BaseFare = -1 },
		"katta per_km":       func(t *Tariff) { t.PerKmRate = maxMoney + 1 },
		"seat_multiplier 0":  func(t *Tariff) { t.SeatMultiplier = 0 },
		"fuel manfiy":        func(t *Tariff) { t.FuelSurcharge = -1 },
		"fuel katta":         func(t *Tariff) { t.FuelSurcharge = 10_001 },
		"komissiya 101%":     func(t *Tariff) { t.CommissionBPS = 10_001 },
		"class yo'q":         func(t *Tariff) { delete(t.CarClassMultipliers, "business") },
		"class 0":            func(t *Tariff) { t.CarClassMultipliers["economy"] = 0 },
		"vehicle yo'q":       func(t *Tariff) { delete(t.VehicleMultipliers, "fura") },
		"vehicle manfiy":     func(t *Tariff) { t.VehicleMultipliers["spark"] = -1000 },
		"vehicle juda katta": func(t *Tariff) { t.VehicleMultipliers["spark"] = maxMultiplier + 1 },
		"min_fare manfiy":    func(t *Tariff) { t.MinFare = -5 },
		"yaxlitlash manfiy":  func(t *Tariff) { t.RoundingStep = -1 },
	}
	for name, f := range mut {
		tr := valid()
		f(&tr)
		if tr.Validate() == nil {
			t.Errorf("%s: xato kutilgan", name)
		}
	}
}
