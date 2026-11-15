package service_test

import (
	"testing"
	"time"

	"github.com/izgo/matching-geo/internal/service"
	"github.com/izgo/matching-geo/internal/store"
)

func TestSpeedJump(t *testing.T) {
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	prev := &store.Location{Lat: 41.3, Lng: 69.2, At: t0}
	// ekvator yaqinida emas, lekin kenglik bo'yicha: 1° = 111 195 m  ->  metr = daraja × 111195
	north := func(m float64) float64 { return 41.3 + m/111195 }

	cases := []struct {
		name    string
		prev    *store.Location
		dMeters float64
		after   time.Duration
		wantSus bool
		wantKMH float64 // taxminan (0 = tekshirilmaydi)
	}{
		{"oldingi nuqta yo'q", nil, 5000, time.Second, false, 0},
		{"oddiy haydash 100 km/s: 278 m / 10 s", prev, 278, 10 * time.Second, false, 100},
		{"288 km/s (chegaradan past)", prev, 800, 10 * time.Second, false, 288},
		{"324 km/s (chegaradan yuqori)", prev, 900, 10 * time.Second, true, 324},
		{"teleport 5 km / 10 s", prev, 5000, 10 * time.Second, true, 1800},
		{"5 km / 70 s = 257 km/s", prev, 5000, 70 * time.Second, false, 257},
		{"5 km / 50 s = 360 km/s", prev, 5000, 50 * time.Second, true, 360},
		{"GPS shovqini: 150 m / 1 s (540 km/s, lekin < 200 m)", prev, 150, time.Second, false, 540},
		{"199 m / 0.1 s (minimal masofadan past)", prev, 199, 100 * time.Millisecond, false, 0},
		{"201 m / 0.1 s (soniyaga yaxlitlanadi: 724 km/s)", prev, 201, 100 * time.Millisecond, true, 724},
		{"bir joyda turibdi", prev, 0, 10 * time.Second, false, 0},
		{"soat orqaga ketgan (dt<=0 -> 1 s)", prev, 1000, -5 * time.Second, true, 3600},
	}
	for _, c := range cases {
		kmh, sus := service.SpeedJump(c.prev, north(c.dMeters), 69.2, t0.Add(c.after), 300, 200)
		if sus != c.wantSus {
			t.Errorf("%s: suspicious=%v (%.0f km/s), kutilgan %v", c.name, sus, kmh, c.wantSus)
		}
		if c.wantKMH > 0 && (kmh < c.wantKMH*0.99 || kmh > c.wantKMH*1.01) {
			t.Errorf("%s: %.1f km/s, kutilgan ~%.0f", c.name, kmh, c.wantKMH)
		}
	}
}
