package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/izgo/pricing/internal/calc"
	"github.com/izgo/pricing/internal/tariff"
	"github.com/izgo/shared/geo"
)

// testToken — faqat test uchun soxta qiymat.
const testToken = "pk.test-token-not-real"

type fixedTariff struct{ t tariff.Tariff }

func (f fixedTariff) Active(context.Context) (tariff.Tariff, error) { return f.t, nil }

// Boshlang'ich tarif (migrations/0002) bilan bir xil: 400 000 + km × 120 000 tiyin × o'rindiq.
func seedTariff() fixedTariff {
	return fixedTariff{tariff.Tariff{
		Version: 1, BaseFare: 400_000, PerKmRate: 120_000, SeatMultiplier: 1000,
		CarClassMultipliers: map[string]int64{"economy": 4000, "comfort": 5000, "business": 6400, "minivan": 6000},
		VehicleMultipliers:  map[string]int64{"spark": 1000, "damas": 1300, "labo": 1500, "isuzu": 2000, "isuzu2": 2500, "fura": 3000},
		RoundingStep:        1, CommissionBPS: 1000,
	}}
}

// Toshkent ichida: to'g'ri chiziq ~3.55 km -> Haversine × 1.3 = 4615 m / 554 s.
func request() Request {
	seats := 1
	return Request{Type: calc.Passenger, Seats: &seats, From: geo.Point{Lat: 41.3111, Lng: 69.2797}, To: geo.Point{Lat: 41.2995, Lng: 69.2401}}
}

// mapboxRouter — Mapbox Directions o'rniga mock HTTP server + Haversine zaxirasi (servisdagi kabi).
func mapboxRouter(t *testing.T, h http.HandlerFunc, onFallback func(error)) geo.Router {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	mb, err := geo.NewMapboxRouter(testToken, srv.URL, &http.Client{Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	hv, err := geo.NewHaversineRouter(1.3, 30)
	if err != nil {
		t.Fatal(err)
	}
	return geo.NewFallbackRouter(mb, hv, time.Minute, onFallback)
}

// Narx Mapbox qaytargan yo'l masofasidan (routes[0].distance) hisoblanadi, davomiylik — routes[0].duration.
func TestCalculateUsesMapboxDistance(t *testing.T) {
	var path string
	router := mapboxRouter(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":6200.4,"duration":930.6}]}`))
	}, func(err error) { t.Errorf("fallback kutilmagan: %v", err) })

	q, err := New(seedTariff(), router).Calculate(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	// 6200 m × 120 000 tiyin/km = 744 000; + 400 000 = 1 144 000
	if q.DistanceM != 6200 || q.DurationS != 931 || q.Price != 1_144_000 || q.Commission != 114_400 {
		t.Errorf("quote = %+v", q)
	}
	if want := "/directions/v5/mapbox/driving/69.279700,41.311100;69.240100,41.299500"; path != want {
		t.Errorf("mapbox path = %q, kutilgan %q", path, want)
	}
	var sum int64
	for _, l := range q.Lines {
		sum += l.Amount
	}
	if sum != q.Price {
		t.Errorf("lines yig'indisi %d != narx %d", sum, q.Price)
	}
}

// Mapbox javob bermasa narx baribir hisoblanadi — Haversine bo'yicha (avvalgi natija: 4615 m, 554 s, 953 800 tiyin).
func TestCalculateFallsBackToHaversine(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"503":       func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
		"401":       func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) },
		"JSON emas": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"NoRoute":   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"code":"NoRoute","routes":[]}`)) },
		"timeout":   func(http.ResponseWriter, *http.Request) { time.Sleep(600 * time.Millisecond) },
	}
	for name, h := range cases {
		var reported error
		router := mapboxRouter(t, h, func(err error) { reported = err })
		q, err := New(seedTariff(), router).Calculate(context.Background(), request())
		if err != nil {
			t.Errorf("%s: narx hisoblanmadi: %v", name, err)
			continue
		}
		if q.DistanceM != 4615 || q.DurationS != 554 || q.Price != 953_800 {
			t.Errorf("%s: quote = %+v", name, q)
		}
		if reported == nil || strings.Contains(reported.Error(), testToken) {
			t.Errorf("%s: xabar %v", name, reported)
		}
	}
}

// Mapbox nosoz bo'lsa har so'rov timeout kutmaydi: tanaffus davomida faqat Haversine.
func TestCalculateSkipsMapboxDuringCooldown(t *testing.T) {
	var calls atomic.Int64
	router := mapboxRouter(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}, nil)
	svc := New(seedTariff(), router)
	for i := 0; i < 4; i++ {
		if q, err := svc.Calculate(context.Background(), request()); err != nil || q.DistanceM != 4615 {
			t.Fatalf("%d: %+v %v", i, q, err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("Mapbox %d marta chaqirildi (1 kutilgan)", calls.Load())
	}
}

type failingRouter struct{}

func (failingRouter) Route(context.Context, geo.Point, geo.Point) (geo.Route, error) {
	return geo.Route{}, errors.New("router yo'q")
}

// Zaxirasiz router xatosi yashirilmaydi (FallbackRouter'siz sozlama).
func TestCalculateRouterErrorIsReturned(t *testing.T) {
	if _, err := New(seedTariff(), failingRouter{}).Calculate(context.Background(), request()); err == nil {
		t.Error("xato kutilgan")
	}
}
