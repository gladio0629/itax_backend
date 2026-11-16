package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/izgo/matching-geo/internal/service"
	"github.com/izgo/matching-geo/internal/store"
	"github.com/izgo/shared/geo"
)

// mapboxToken — faqat test uchun soxta qiymat.
const mapboxToken = "pk.test-token-not-real"

// mapboxEnv — Mapbox Directions o'rniga mock HTTP server; zaxira — Haversine (servisdagi kabi).
func mapboxEnv(t *testing.T, h http.HandlerFunc, opts ...func(*store.Options, *service.Options)) *env {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	mb, err := geo.NewMapboxRouter(mapboxToken, srv.URL, &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	hv, _ := geo.NewHaversineRouter(1.3, 30)
	return newEnvWithRouter(t, geo.NewFallbackRouter(mb, hv, time.Minute, nil), opts...)
}

func etas(t *testing.T, r resp) map[string]float64 {
	t.Helper()
	if r.Status != 200 {
		t.Fatalf("find: %d %s", r.Status, r.Raw)
	}
	out := map[string]float64{}
	for _, d := range r.drivers() {
		out[d["driver_id"].(string)] = d["pickup_eta_s"].(float64)
	}
	return out
}

// /find: pickup_eta_s Mapbox Directions javobidan (routes[0].duration); so'rov haydovchidan yo'lovchigacha.
func TestFindETAFromMapbox(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
	)
	e := mapboxEnv(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		// shimoldagi nomzodlar: uzoqroq haydovchi — kattaroq vaqt (kenglik yo'lning birinchi nuqtasida)
		if strings.Contains(r.URL.Path, ",41.32") { // ~1 km
			_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":1900,"duration":312.4}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":14200,"duration":1525.7}]}`))
	})
	near, far := e.driver(1, nil), e.driver(10, nil)

	got := etas(t, e.call("POST", "/internal/matching/find", findReq(nil)))
	if got[near.String()] != 312 || got[far.String()] != 1526 {
		t.Errorf("ETA Mapbox'dan emas: %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 {
		t.Fatalf("Mapbox %d marta chaqirildi (2 kutilgan): %v", len(paths), paths)
	}
	for _, p := range paths {
		// {haydovchi lng},{lat};{yo'lovchi lng},{lat} (haydovchi nuqtasi Redis GEO aniqligida: ~0.5 m)
		if !strings.HasPrefix(p, "/directions/v5/mapbox/driving/69.2797") || !strings.HasSuffix(p, ";69.279700,41.311100") {
			t.Errorf("path = %q", p)
		}
	}
}

// /route ham Mapbox'dan: Pricing bilan bir xil manba.
func TestRouteFromMapbox(t *testing.T) {
	e := mapboxEnv(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":6200.4,"duration":930.6}]}`))
	})
	r := e.call("POST", "/internal/matching/route", map[string]any{
		"from": map[string]any{"lat": 41.3111, "lng": 69.2797}, "to": map[string]any{"lat": 41.2995, "lng": 69.2401}})
	if r.Status != 200 || r.Body["distance_m"] != float64(6200) || r.Body["duration_s"] != float64(931) {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

// Mapbox nosoz: /find baribir javob beradi, ETA Haversine bo'yicha; tanaffusda Mapbox qayta chaqirilmaydi.
func TestFindFallsBackToHaversineWhenMapboxDown(t *testing.T) {
	var calls atomic.Int64
	e := mapboxEnv(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	plain := newEnv(t) // faqat Haversine: kutilgan qiymatlar manbai
	want := map[float64]float64{}
	for _, km := range []float64{1, 10, 30} {
		id := plain.driver(km, nil)
		want[km] = etas(t, plain.call("POST", "/internal/matching/find", findReq(nil)))[id.String()]
	}

	ids := map[float64]string{}
	for _, km := range []float64{1, 10, 30} {
		ids[km] = e.driver(km, nil).String()
	}
	got := etas(t, e.call("POST", "/internal/matching/find", findReq(nil)))
	for km, id := range ids {
		if got[id] != want[km] || got[id] <= 0 {
			t.Errorf("%v km: ETA %v, Haversine bo'yicha %v kutilgan", km, got[id], want[km])
		}
	}
	first := calls.Load()
	if first < 1 || first > 3 {
		t.Errorf("Mapbox %d marta chaqirildi (1..3 kutilgan)", first)
	}
	etas(t, e.call("POST", "/internal/matching/find", findReq(nil)))
	if calls.Load() != first {
		t.Errorf("tanaffusda Mapbox yana chaqirildi: %d -> %d", first, calls.Load())
	}
}

// Mapbox sekin: /find ETABudget ichida javob beradi (ulgurmagan nomzodlar — Haversine), so'rov osilib qolmaydi.
func TestFindETABudget(t *testing.T) {
	release := make(chan struct{})
	e := mapboxEnv(t, func(http.ResponseWriter, *http.Request) { <-release },
		func(_ *store.Options, o *service.Options) { o.ETABudget, o.ETAConcurrency = 150*time.Millisecond, 2 })
	t.Cleanup(func() { close(release) })
	for _, km := range []float64{1, 5, 10, 20, 30} {
		e.driver(km, nil)
	}
	start := time.Now()
	got := etas(t, e.call("POST", "/internal/matching/find", findReq(nil)))
	if took := time.Since(start); took > time.Second {
		t.Errorf("find %v davom etdi (ETABudget 150 ms)", took)
	}
	if len(got) != 5 {
		t.Fatalf("%d ta natija (5 kutilgan)", len(got))
	}
	for id, eta := range got {
		if eta <= 0 {
			t.Errorf("%s: ETA %v", id, eta)
		}
	}
}
