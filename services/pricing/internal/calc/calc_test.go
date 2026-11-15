package calc

import (
	"errors"
	"math"
	"testing"

	"github.com/izgo/pricing/internal/tariff"
)

// Sodda tarif: 10 km da omil f‰ -> km qismi = f × 1000 tiyin.
func base() tariff.Tariff {
	return tariff.Tariff{
		Version: 1, BaseFare: 400_000, PerKmRate: 100_000, SeatMultiplier: 1000,
		CarClassMultipliers: map[string]int64{"economy": 4000, "comfort": 5000, "business": 6400, "minivan": 6000},
		VehicleMultipliers:  map[string]int64{"spark": 1000, "damas": 1300, "labo": 1500, "isuzu": 2000, "isuzu2": 2500, "fura": 3000},
		RoundingStep:        1, CommissionBPS: 1000,
	}
}

func ip(v int) *int       { return &v }
func sp(v string) *string { return &v }

func price(t *testing.T, tr tariff.Tariff, in Input) Result {
	t.Helper()
	r, err := Price(tr, in)
	if err != nil {
		t.Fatalf("%+v: %v", in, err)
	}
	return r
}

func TestPassengerSeats(t *testing.T) {
	// baseFare + distance × perKm × seatCount: 400 000 + 10 km × 100 000 × n
	for seats, want := range map[int]int64{1: 1_400_000, 2: 2_400_000, 3: 3_400_000, 4: 4_400_000} {
		r := price(t, base(), Input{Type: Passenger, Seats: ip(seats), DistanceM: 10_000})
		if r.Price != want {
			t.Errorf("seats=%d: %d, kutilgan %d", seats, r.Price, want)
		}
		if r.Commission != want/10 {
			t.Errorf("seats=%d komissiya %d", seats, r.Commission)
		}
	}
}

func TestSeatMultiplierScalesSeats(t *testing.T) {
	tr := base()
	tr.SeatMultiplier = 500 // har bir o'rindiq 0.5×
	if r := price(t, tr, Input{Type: Passenger, Seats: ip(2), DistanceM: 10_000}); r.Price != 1_400_000 {
		t.Errorf("seat_multiplier=0.5, seats=2: %d", r.Price)
	}
	tr.SeatMultiplier = 1500
	if r := price(t, tr, Input{Type: Passenger, Seats: ip(2), DistanceM: 10_000}); r.Price != 400_000+3_000_000 {
		t.Errorf("seat_multiplier=1.5, seats=2: %d", r.Price)
	}
}

func TestPassengerFullCar(t *testing.T) {
	for class, want := range map[string]int64{"economy": 4_400_000, "comfort": 5_400_000, "business": 6_800_000, "minivan": 6_400_000} {
		r := price(t, base(), Input{Type: Passenger, CarClass: sp(class), DistanceM: 10_000})
		if r.Price != want {
			t.Errorf("%s: %d, kutilgan %d", class, r.Price, want)
		}
	}
}

func TestDeliveryVehicles(t *testing.T) {
	// spark=1.0, damas=1.3, labo=1.5, isuzu=2.0, isuzu2=2.5, fura=3.0
	for v, want := range map[string]int64{
		"spark": 1_400_000, "damas": 1_700_000, "labo": 1_900_000, "isuzu": 2_400_000, "isuzu2": 2_900_000, "fura": 3_400_000,
	} {
		r := price(t, base(), Input{Type: Delivery, VehicleType: sp(v), DistanceM: 10_000})
		if r.Price != want {
			t.Errorf("%s: %d, kutilgan %d", v, r.Price, want)
		}
	}
}

func TestZeroDistanceIsBaseFare(t *testing.T) {
	if r := price(t, base(), Input{Type: Passenger, Seats: ip(3)}); r.Price != 400_000 {
		t.Errorf("%d", r.Price)
	}
}

func TestFuelSurchargeMinFareRounding(t *testing.T) {
	in := Input{Type: Passenger, Seats: ip(1), DistanceM: 10_000} // 1 400 000

	tr := base()
	tr.FuelSurcharge = 100 // +10%
	if r := price(t, tr, in); r.Price != 1_540_000 {
		t.Errorf("fuel +10%%: %d", r.Price)
	}

	tr = base()
	tr.MinFare = 2_000_000
	if r := price(t, tr, in); r.Price != 2_000_000 {
		t.Errorf("min_fare: %d", r.Price)
	}
	tr.MinFare = 1_000_000 // narx minimaldan yuqori — o'zgarmaydi
	if r := price(t, tr, in); r.Price != 1_400_000 {
		t.Errorf("min_fare pastroq: %d", r.Price)
	}

	// yaxlitlash qadami 10 000 tiyin (100 so'm), yarmi yuqoriga
	tr = base()
	tr.RoundingStep = 10_000
	for dist, want := range map[int]int64{
		1234: 520_000, // 523 400 -> 520 000
		1236: 520_000, // 523 600 -> 520 000
		1250: 530_000, // 525 000 -> yarmi yuqoriga 530 000
		1500: 550_000, // aniq
		1253: 530_000, // 525 300 -> 530 000
	} {
		if r := price(t, tr, Input{Type: Passenger, Seats: ip(1), DistanceM: dist}); r.Price != want {
			t.Errorf("%d m: %d, kutilgan %d", dist, r.Price, want)
		}
	}
	// tartib: ustama -> minimal narx -> yaxlitlash
	tr = base()
	tr.FuelSurcharge, tr.MinFare, tr.RoundingStep = 100, 1_545_000, 100_000
	if r := price(t, tr, in); r.Price != 1_500_000 { // 1 540 000 -> min 1 545 000 -> 1 500 000 (15.45 -> 15)
		t.Errorf("tartib: %d", r.Price)
	}
}

func TestHalfUpDivision(t *testing.T) {
	tr := base()
	tr.BaseFare, tr.PerKmRate, tr.CommissionBPS = 0, 1000, 0
	// 1 m × 1000 tiyin/km × 0.5 = 0.5 tiyin -> 1
	if r := price(t, tr, Input{Type: Delivery, VehicleType: sp("spark"), DistanceM: 1}); r.Price != 1 { // 1×1000×1000/1e6 = 1
		t.Errorf("%d", r.Price)
	}
	tr.VehicleMultipliers["spark"] = 500
	if r := price(t, tr, Input{Type: Delivery, VehicleType: sp("spark"), DistanceM: 1}); r.Price != 1 { // 0.5 -> 1
		t.Errorf("0.5 yarmi yuqoriga: %d", r.Price)
	}
	tr.PerKmRate = 999
	if r := price(t, tr, Input{Type: Delivery, VehicleType: sp("spark"), DistanceM: 1}); r.Price != 0 { // 0.4995 -> 0
		t.Errorf("0.4995: %d", r.Price)
	}
}

func TestCommission(t *testing.T) {
	tr := base()
	tr.CommissionBPS = 1250 // 12.5%
	r := price(t, tr, Input{Type: Passenger, Seats: ip(1), DistanceM: 10_000})
	if r.Commission != 175_000 {
		t.Errorf("12.5%% of 1 400 000: %d", r.Commission)
	}
	tr.CommissionBPS = 0
	if r := price(t, tr, Input{Type: Passenger, Seats: ip(1), DistanceM: 10_000}); r.Commission != 0 {
		t.Errorf("0%%: %d", r.Commission)
	}
	tr.CommissionBPS = 10_000
	if r := price(t, tr, Input{Type: Passenger, Seats: ip(1), DistanceM: 10_000}); r.Commission != r.Price {
		t.Errorf("100%%: %d != %d", r.Commission, r.Price)
	}
}

func TestInvalidInputs(t *testing.T) {
	cases := map[string]Input{
		"seats 0":               {Type: Passenger, Seats: ip(0)},
		"seats 5":               {Type: Passenger, Seats: ip(5)},
		"seats va car_class":    {Type: Passenger, Seats: ip(1), CarClass: sp("economy")},
		"hech biri":             {Type: Passenger},
		"noma'lum car_class":    {Type: Passenger, CarClass: sp("vip")},
		"passenger + vehicle":   {Type: Passenger, Seats: ip(1), VehicleType: sp("labo")},
		"delivery + seats":      {Type: Delivery, VehicleType: sp("labo"), Seats: ip(1)},
		"delivery + car_class":  {Type: Delivery, VehicleType: sp("labo"), CarClass: sp("economy")},
		"delivery vehicle yo'q": {Type: Delivery},
		"noma'lum vehicle":      {Type: Delivery, VehicleType: sp("bus")},
		"noma'lum type":         {Type: "taxi", Seats: ip(1)},
		"manfiy masofa":         {Type: Passenger, Seats: ip(1), DistanceM: -1},
	}
	for name, in := range cases {
		if _, err := Price(base(), in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// math/big: oraliq natijalar int64 dan oshsa ham panic/aylanib ketish yo'q.
func TestHugeValuesDoNotOverflow(t *testing.T) {
	tr := base()
	tr.PerKmRate = 1_000_000_000_000
	tr.SeatMultiplier = 100_000
	_, err := Price(tr, Input{Type: Passenger, Seats: ip(4), DistanceM: math.MaxInt32})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("juda katta narx rad etilishi kerak: %v", err)
	}
	// katta, lekin sig'adigan: 26 000 km, 1 000 000 so'm/km, 6.4×
	tr = base()
	tr.PerKmRate = 100_000_000
	r, err := Price(tr, Input{Type: Passenger, CarClass: sp("business"), DistanceM: 26_000_000})
	if err != nil || r.Price != 400_000+26_000*100_000_000*64/10 {
		t.Errorf("%+v %v", r, err)
	}
}

func TestPriceMonotonicInDistance(t *testing.T) {
	var last int64 = -1
	for d := 0; d <= 50_000; d += 1_000 {
		p := price(t, base(), Input{Type: Delivery, VehicleType: sp("labo"), DistanceM: d}).Price
		if p < last {
			t.Fatalf("narx masofa oshganda kamaydi: %d m", d)
		}
		last = p
	}
}
