package domain

import "testing"

func TestOrderTransitions(t *testing.T) {
	all := []OrderStatus{Searching, Accepted, InProgress, Completed, Cancelled, Expired}
	allowed := map[[2]OrderStatus]bool{
		{Searching, Accepted}:   true,
		{Searching, Cancelled}:  true,
		{Searching, Expired}:    true,
		{Accepted, InProgress}:  true,
		{InProgress, Completed}: true,
	}
	for _, from := range all {
		for _, to := range all {
			if got := from.CanTransitionTo(to); got != allowed[[2]OrderStatus{from, to}] {
				t.Errorf("%s -> %s: got %v", from, to, got)
			}
		}
	}
	for _, s := range []OrderStatus{Completed, Cancelled, Expired} {
		if !s.Terminal() || s.Active() {
			t.Errorf("%s terminal bo'lishi kerak", s)
		}
	}
	for _, s := range []OrderStatus{Searching, Accepted, InProgress} {
		if s.Terminal() || !s.Active() {
			t.Errorf("%s faol bo'lishi kerak", s)
		}
	}
}

func TestTripTransitions(t *testing.T) {
	all := []TripStatus{TripAssigned, TripInProgress, TripCompleted, TripCancelled}
	allowed := map[[2]TripStatus]bool{{TripAssigned, TripInProgress}: true, {TripInProgress, TripCompleted}: true}
	for _, from := range all {
		for _, to := range all {
			if got := from.CanTransitionTo(to); got != allowed[[2]TripStatus{from, to}] {
				t.Errorf("%s -> %s: got %v", from, to, got)
			}
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestCheckShape(t *testing.T) {
	cases := []struct {
		name string
		o    Order
		ok   bool
	}{
		{"passenger seats", Order{Type: TypePassenger, Seats: ptr(2)}, true},
		{"passenger car_class", Order{Type: TypePassenger, CarClass: ptr(Comfort)}, true},
		{"passenger ikkalasi", Order{Type: TypePassenger, Seats: ptr(2), CarClass: ptr(Comfort)}, false},
		{"passenger hech biri", Order{Type: TypePassenger}, false},
		{"passenger seats 5", Order{Type: TypePassenger, Seats: ptr(5)}, false},
		{"passenger seats 0", Order{Type: TypePassenger, Seats: ptr(0)}, false},
		{"passenger car_class noto'g'ri", Order{Type: TypePassenger, CarClass: ptr(CarClass("vip"))}, false},
		{"passenger + vehicle", Order{Type: TypePassenger, Seats: ptr(1), VehicleType: ptr(Labo)}, false},
		{"delivery", Order{Type: TypeDelivery, VehicleType: ptr(Fura)}, true},
		{"delivery vehicle yo'q", Order{Type: TypeDelivery}, false},
		{"delivery noto'g'ri vehicle", Order{Type: TypeDelivery, VehicleType: ptr(VehicleType("bus"))}, false},
		{"delivery + seats", Order{Type: TypeDelivery, VehicleType: ptr(Spark), Seats: ptr(1)}, false},
		{"delivery + car_class", Order{Type: TypeDelivery, VehicleType: ptr(Spark), CarClass: ptr(Economy)}, false},
		{"noma'lum type", Order{Type: "taxi"}, false},
	}
	for _, c := range cases {
		if err := c.o.CheckShape(); (err == nil) != c.ok {
			t.Errorf("%s: err=%v", c.name, err)
		}
	}
}

func TestValidateRating(t *testing.T) {
	k := func(r ReasonKind) *ReasonKind { return &r }
	s := func(v string) *string { return &v }
	long := make([]rune, 201)
	for i := range long {
		long[i] = 'я'
	}
	two00 := string(long[:200])

	cases := []struct {
		name  string
		stars int
		kind  *ReasonKind
		text  *string
		bad   string // kutilgan xato maydoni ("" = yaroqli)
	}{
		{"1 yulduz", 1, nil, nil, ""},
		{"4 yulduz", 4, nil, nil, ""},
		{"5 + polite", 5, k(ReasonPolite), nil, ""},
		{"5 + good_talk", 5, k(ReasonGoodTalk), nil, ""},
		{"5 + comfort", 5, k(ReasonComfort), nil, ""},
		{"5 + just_because", 5, k(ReasonJustBecause), nil, ""},
		{"5 + custom + matn", 5, k(ReasonCustom), s("rahmat"), ""},
		{"5 + custom + 200 belgi", 5, k(ReasonCustom), &two00, ""},
		{"0", 0, nil, nil, "stars"},
		{"6", 6, nil, nil, "stars"},
		{"5, sabab yo'q", 5, nil, nil, "reason"},
		{"5 + noto'g'ri kind", 5, k("rude"), nil, "reason.kind"},
		{"5 + custom, matn yo'q", 5, k(ReasonCustom), nil, "reason.custom_text"},
		{"5 + custom, bo'sh matn", 5, k(ReasonCustom), s(""), "reason.custom_text"},
		{"5 + custom, 201 belgi", 5, k(ReasonCustom), s(string(long)), "reason.custom_text"},
		{"5 + polite + matn", 5, k(ReasonPolite), s("x"), "reason.custom_text"},
		{"3 + sabab", 3, k(ReasonPolite), nil, "reason"},
		{"3 + matn", 3, nil, s("x"), "reason"},
	}
	for _, c := range cases {
		f := ValidateRating(c.stars, c.kind, c.text)
		if c.bad == "" && len(f) != 0 {
			t.Errorf("%s: kutilmagan xato %v", c.name, f)
		}
		if c.bad != "" && f[c.bad] == "" {
			t.Errorf("%s: %q xatosi kutilgan, got %v", c.name, c.bad, f)
		}
	}
}
