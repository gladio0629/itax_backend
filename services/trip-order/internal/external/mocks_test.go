package external

import (
	"context"
	"testing"

	"github.com/izgo/trip-order/internal/domain"
)

func p(lat, lng float64) domain.Point { return domain.Point{Lat: lat, Lng: lng} }
func ip(v int) *int                   { return &v }

func quote(t *testing.T, r QuoteRequest) Quote {
	t.Helper()
	q, err := MockPricer{}.Quote(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestMockPricer(t *testing.T) {
	from, near, far := p(41.3111, 69.2797), p(41.2995, 69.2401), p(41.0, 68.9)
	base := QuoteRequest{Type: domain.TypePassenger, Seats: ip(4), From: from, To: near}

	a := quote(t, base)
	if a != quote(t, base) {
		t.Error("deterministik emas")
	}
	if a.Price%roundTo != 0 || a.Price < minFare || a.Commission != a.Price/10 || a.DistanceM <= 0 || a.DurationS <= 0 {
		t.Errorf("quote: %+v", a)
	}
	// uzoqroq — qimmatroq
	far4 := quote(t, QuoteRequest{Type: domain.TypePassenger, Seats: ip(4), From: from, To: far})
	if far4.Price <= a.Price || far4.DistanceM <= a.DistanceM {
		t.Errorf("uzoq yo'l: %+v <= %+v", far4, a)
	}
	// o'rindiqlar soni oshgani sari narx oshadi
	var last int64
	for s := 1; s <= 4; s++ {
		q := quote(t, QuoteRequest{Type: domain.TypePassenger, Seats: ip(s), From: from, To: far})
		if q.Price <= last {
			t.Errorf("seats=%d narx %d <= %d", s, q.Price, last)
		}
		last = q.Price
	}
	// minimal narx
	tiny := quote(t, QuoteRequest{Type: domain.TypePassenger, Seats: ip(1), From: from, To: p(41.3112, 69.2798)})
	if tiny.Price != minFare {
		t.Errorf("minimal narx: %d", tiny.Price)
	}
	// butun mashina: class tartibi
	cc := func(c domain.CarClass) int64 {
		return quote(t, QuoteRequest{Type: domain.TypePassenger, CarClass: &c, From: from, To: far}).Price
	}
	if !(cc(domain.Economy) < cc(domain.Comfort) && cc(domain.Comfort) < cc(domain.Business)) {
		t.Error("car_class narxlari tartibi")
	}
	// xatolar
	for name, r := range map[string]QuoteRequest{
		"seats/class yo'q":      {Type: domain.TypePassenger, From: from, To: near},
		"delivery vehicle yo'q": {Type: domain.TypeDelivery, From: from, To: near},
		"noma'lum type":         {Type: "x", From: from, To: near},
		"seats 9":               {Type: domain.TypePassenger, Seats: ip(9), From: from, To: near},
	} {
		if _, err := (MockPricer{}).Quote(context.Background(), r); err == nil {
			t.Errorf("%s: xato kutilgan", name)
		}
	}
	// Finalize kelishilgan narxni o'zgartirmaydi
	f, _ := MockPricer{}.Finalize(context.Background(), FinalizeRequest{Quoted: a})
	if f.Price != a.Price || f.Commission != a.Commission || f.DistanceM != a.DistanceM {
		t.Errorf("finalize: %+v", f)
	}
}
