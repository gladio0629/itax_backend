package geo

import (
	"context"
	"math"
	"testing"
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.3f, kutilgan %.3f (±%.3f)", name, got, want, tol)
	}
}

func TestHaversine(t *testing.T) {
	cases := []struct {
		name string
		a, b Point
		want float64 // metr (R = 6 371 000)
		tol  float64
	}{
		{"bir xil nuqta", Point{41.3, 69.2}, Point{41.3, 69.2}, 0, 0},
		{"1 daraja kenglik", Point{0, 0}, Point{1, 0}, 111194.927, 0.01},
		{"ekvatorda 1 daraja uzunlik", Point{0, 0}, Point{0, 1}, 111194.927, 0.01},
		{"antipod (ekvator)", Point{0, 0}, Point{0, 180}, 20015086.796, 0.01},
		{"qutbdan qutbgacha", Point{90, 0}, Point{-90, 0}, 20015086.796, 0.01},
		{"London - Parij", Point{51.5074, -0.1278}, Point{48.8566, 2.3522}, 343556.06, 0.5},
		{"Toshkent - Samarqand", Point{41.2995, 69.2401}, Point{39.6542, 66.9597}, 265826.13, 0.5},
		{"Toshkent ichida", Point{41.3111, 69.2797}, Point{41.2995, 69.2401}, 3550.38, 0.05},
		{"180-meridian orqali", Point{0, 179.5}, Point{0, -179.5}, 111194.927, 0.01},
	}
	for _, c := range cases {
		got := Haversine(c.a, c.b)
		near(t, c.name, got, c.want, c.tol)
		near(t, c.name+" (teskari)", Haversine(c.b, c.a), got, 1e-9) // simmetriya
	}
}

func TestHaversineRouter(t *testing.T) {
	r, err := NewHaversineRouter(1.3, 30)
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := r.Route(context.Background(), Point{41.3111, 69.2797}, Point{41.2995, 69.2401})
	// 3550.38 × 1.3 = 4615.49 -> 4615 m; 4615 / (30/3.6) = 553.8 -> 554 s
	if rt.DistanceM != 4615 || rt.DurationS != 554 {
		t.Errorf("route = %+v", rt)
	}
	z, _ := r.Route(context.Background(), Point{1, 1}, Point{1, 1})
	if z.DistanceM != 0 || z.DurationS != 0 {
		t.Errorf("nol masofa: %+v", z)
	}
	// koeffitsient haqiqatan ishlaydi
	r2, _ := NewHaversineRouter(1.0, 30)
	a, _ := r2.Route(context.Background(), Point{0, 0}, Point{0, 1})
	b, _ := r.Route(context.Background(), Point{0, 0}, Point{0, 1})
	if a.DistanceM != 111195 || b.DistanceM != 144553 { // 111194.93 -> 111195; ×1.3 = 144553.4
		t.Errorf("koeffitsient: %d / %d", a.DistanceM, b.DistanceM)
	}
	for _, bad := range [][2]float64{{0.5, 30}, {3.5, 30}, {1.3, 0}, {1.3, 250}, {1.3, -5}} {
		if _, err := NewHaversineRouter(bad[0], bad[1]); err == nil {
			t.Errorf("%v: xato kutilgan", bad)
		}
	}
}
