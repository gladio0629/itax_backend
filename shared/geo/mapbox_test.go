package geo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testToken — faqat test uchun soxta qiymat (haqiqiy token hech qachon kodga yozilmaydi).
const testToken = "pk.test-token-not-real"

var (
	tashkentA = Point{Lat: 41.3111, Lng: 69.2797}
	tashkentB = Point{Lat: 41.2995, Lng: 69.2401}
)

// mockMapbox — Mapbox Directions o'rniga httptest serveri.
func mockMapbox(t *testing.T, h http.HandlerFunc) *MapboxRouter {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	m, err := NewMapboxRouter(testToken, srv.URL, &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMapboxRouterRequestAndResponse(t *testing.T) {
	var gotPath, gotQuery, gotMethod string
	m := mockMapbox(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		// routes[0] ishlatiladi; muqobil marshrut (routes[1]) e'tiborga olinmaydi
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":5321.6,"duration":812.4},{"distance":9999,"duration":9999}],"waypoints":[]}`))
	})
	rt, err := m.Route(context.Background(), tashkentA, tashkentB)
	if err != nil {
		t.Fatal(err)
	}
	if rt.DistanceM != 5322 || rt.DurationS != 812 {
		t.Errorf("route = %+v, kutilgan 5322 m / 812 s", rt)
	}
	// {lon1},{lat1};{lon2},{lat2} — avval uzunlik
	if want := "/directions/v5/mapbox/driving/69.279700,41.311100;69.240100,41.299500"; gotPath != want {
		t.Errorf("path = %q, kutilgan %q", gotPath, want)
	}
	if gotMethod != http.MethodGet || !strings.Contains(gotQuery, "access_token="+testToken) {
		t.Errorf("so'rov: %s ?%s", gotMethod, gotQuery)
	}
}

func TestMapboxRouterErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		noRoute bool
	}{
		{"401 token", 401, `{"message":"Not Authorized - Invalid Token"}`, false},
		{"429 limit", 429, `{"message":"Too Many Requests"}`, false},
		{"500", 500, `oops`, false},
		{"200 JSON emas", 200, `<html>`, false},
		{"200 NoRoute", 200, `{"code":"NoRoute","routes":[]}`, true},
		{"200 routes bo'sh", 200, `{"code":"Ok","routes":[]}`, true},
		{"422 NoSegment", 422, `{"code":"NoSegment","message":"Could not find a matching segment"}`, true},
		{"manfiy masofa", 200, `{"code":"Ok","routes":[{"distance":-1,"duration":5}]}`, false},
	}
	for _, c := range cases {
		m := mockMapbox(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		})
		_, err := m.Route(context.Background(), tashkentA, tashkentB)
		if err == nil {
			t.Errorf("%s: xato kutilgan", c.name)
			continue
		}
		if errors.Is(err, ErrNoRoute) != c.noRoute {
			t.Errorf("%s: ErrNoRoute=%v, kutilgan %v (%v)", c.name, !c.noRoute, c.noRoute, err)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Errorf("%s: xato matnida token bor: %v", c.name, err)
		}
	}
}

// Tarmoq xatosi va timeout: *url.Error to'liq manzilni (access_token bilan) saqlaydi — xato matniga chiqmasligi kerak.
func TestMapboxRouterNeverLeaksToken(t *testing.T) {
	// 1) ulanib bo'lmaydi (server yopilgan)
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	m, err := NewMapboxRouter(testToken, url, &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Route(context.Background(), tashkentA, tashkentB); err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "access_token") {
		t.Errorf("ulanish xatosi: %v", err)
	}

	// 2) klient timeouti
	release := make(chan struct{})
	slow := mockMapbox(t, func(http.ResponseWriter, *http.Request) { <-release })
	t.Cleanup(func() { close(release) })
	slow.client = &http.Client{Timeout: 50 * time.Millisecond}
	if _, err = slow.Route(context.Background(), tashkentA, tashkentB); err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "access_token") {
		t.Errorf("timeout xatosi: %v", err)
	}

	// 3) kontekst muddati: sabab saqlanadi (errors.Is), token yo'q
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	slow.client = &http.Client{Timeout: time.Second}
	_, err = slow.Route(ctx, tashkentA, tashkentB)
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), testToken) {
		t.Errorf("kontekst xatosi: %v", err)
	}

	// 4) server javobida token qaytsa ham yashiriladi
	echo := mockMapbox(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"message":"bad token ` + r.URL.Query().Get("access_token") + `"}`))
	})
	if _, err = echo.Route(context.Background(), tashkentA, tashkentB); err == nil || strings.Contains(err.Error(), testToken) {
		t.Errorf("javobdagi token: %v", err)
	}
}

func TestNewMapboxRouterValidation(t *testing.T) {
	if _, err := NewMapboxRouter("  ", "", nil); err == nil {
		t.Error("bo'sh token: xato kutilgan")
	}
	if _, err := NewMapboxRouter(testToken, "ftp://x", nil); err == nil {
		t.Error("noto'g'ri manzil: xato kutilgan")
	}
	m, err := NewMapboxRouter(" "+testToken+"\n", "", nil)
	if err != nil || m.baseURL != MapboxBaseURL || m.client.Timeout != MapboxTimeout || m.token != testToken {
		t.Errorf("default: %+v %v", m, err)
	}
}

// ---------------------------------------------------------------- fallback

type stubRouter struct {
	calls atomic.Int64
	rt    Route
	err   error
}

func (s *stubRouter) Route(context.Context, Point, Point) (Route, error) {
	s.calls.Add(1)
	return s.rt, s.err
}

func haversine(t *testing.T) HaversineRouter {
	t.Helper()
	hv, err := NewHaversineRouter(1.3, 30)
	if err != nil {
		t.Fatal(err)
	}
	return hv
}

func TestFallbackRouterUsesPrimaryWhenHealthy(t *testing.T) {
	primary := &stubRouter{rt: Route{DistanceM: 5322, DurationS: 812}}
	f := NewFallbackRouter(primary, haversine(t), time.Minute, func(err error) { t.Errorf("fallback kutilmagan: %v", err) })
	rt, err := f.Route(context.Background(), tashkentA, tashkentB)
	if err != nil || rt.DistanceM != 5322 || rt.DurationS != 812 {
		t.Errorf("route = %+v %v", rt, err)
	}
}

// Mapbox javob bermasa — Haversine (4615 m / 554 s); tanaffus davomida Mapbox chaqirilmaydi, keyin yana uriniladi.
func TestFallbackRouterCooldown(t *testing.T) {
	primary := &stubRouter{err: errors.New("mapbox: status 503")}
	var reported int
	f := NewFallbackRouter(primary, haversine(t), 30*time.Second, func(error) { reported++ })
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return now }

	for i := 0; i < 5; i++ {
		rt, err := f.Route(context.Background(), tashkentA, tashkentB)
		if err != nil || rt.DistanceM != 4615 || rt.DurationS != 554 {
			t.Fatalf("fallback natijasi: %+v %v", rt, err)
		}
	}
	if primary.calls.Load() != 1 || reported != 1 {
		t.Errorf("tanaffusda Mapbox %d marta chaqirildi, %d marta xabar berildi (1/1 kutilgan)", primary.calls.Load(), reported)
	}

	// tanaffus tugadi: Mapbox tuzalgan bo'lsa yana ishlatiladi
	now = now.Add(31 * time.Second)
	primary.err, primary.rt = nil, Route{DistanceM: 5000, DurationS: 700}
	if rt, _ := f.Route(context.Background(), tashkentA, tashkentB); rt.DistanceM != 5000 || primary.calls.Load() != 2 {
		t.Errorf("tanaffusdan keyin: %+v, chaqiruvlar %d", rt, primary.calls.Load())
	}
}

// "Yo'l topilmadi" va chaqiruvchi konteksti bekor bo'lishi tanaffusga sabab bo'lmaydi.
func TestFallbackRouterNoCooldownForNoRouteOrCancel(t *testing.T) {
	primary := &stubRouter{err: ErrNoRoute}
	f := NewFallbackRouter(primary, haversine(t), time.Minute, nil)
	for i := 0; i < 3; i++ {
		if rt, err := f.Route(context.Background(), tashkentA, tashkentB); err != nil || rt.DistanceM != 4615 {
			t.Fatalf("NoRoute: %+v %v", rt, err)
		}
	}
	if primary.calls.Load() != 3 {
		t.Errorf("NoRoute: Mapbox %d marta chaqirildi (3 kutilgan)", primary.calls.Load())
	}

	cancelled := &stubRouter{err: context.Canceled}
	f = NewFallbackRouter(cancelled, haversine(t), time.Minute, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rt, err := f.Route(ctx, tashkentA, tashkentB); err != nil || rt.DistanceM != 4615 {
		t.Errorf("bekor qilingan: %+v %v", rt, err)
	}
	if _, _ = f.Route(context.Background(), tashkentA, tashkentB); cancelled.calls.Load() != 2 {
		t.Errorf("bekor qilishdan keyin Mapbox yana chaqirilishi kerak: %d", cancelled.calls.Load())
	}
}

// To'liq zanjir: MapboxRouter (mock HTTP) + FallbackRouter.
func TestFallbackRouterWithMockHTTP(t *testing.T) {
	var fail atomic.Bool
	m := mockMapbox(t, func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":5322,"duration":812}]}`))
	})
	var last error
	f := NewFallbackRouter(m, haversine(t), time.Minute, func(err error) { last = err })
	if rt, _ := f.Route(context.Background(), tashkentA, tashkentB); rt.DistanceM != 5322 || rt.DurationS != 812 {
		t.Errorf("mapbox: %+v", rt)
	}
	fail.Store(true)
	rt, err := f.Route(context.Background(), tashkentA, tashkentB)
	if err != nil || rt.DistanceM != 4615 || rt.DurationS != 554 {
		t.Errorf("fallback: %+v %v", rt, err)
	}
	if last == nil || !strings.Contains(last.Error(), "503") || strings.Contains(last.Error(), testToken) {
		t.Errorf("xabar: %v", last)
	}
}

func TestNewRouterSelection(t *testing.T) {
	cases := []struct {
		mode, token string
		mapbox      bool
	}{
		{"", "", false},            // token yo'q — Haversine
		{"real", "", false},        // real, lekin token yo'q — Haversine
		{"mock", testToken, false}, // ataylab o'chirilgan
		{"MOCK ", testToken, false},
		{"", testToken, true}, // avtomatik: token bor
		{"real", testToken, true},
		{" Real", " " + testToken + " ", true},
	}
	for _, c := range cases {
		r, mapbox, err := NewRouter(Config{RoadFactor: 1.3, AvgSpeedKMH: 30, MapboxMode: c.mode, MapboxToken: c.token})
		if err != nil || mapbox != c.mapbox {
			t.Errorf("mode=%q token=%v: mapbox=%v err=%v", c.mode, c.token != "", mapbox, err)
			continue
		}
		if _, isFallback := r.(*FallbackRouter); isFallback != c.mapbox {
			t.Errorf("mode=%q: router turi %T", c.mode, r)
		}
		if !c.mapbox {
			// Mapbox'siz natija avvalgidek (Pricing/Matching testlaridagi 4615 m / 554 s)
			if rt, _ := r.Route(context.Background(), tashkentA, tashkentB); rt.DistanceM != 4615 || rt.DurationS != 554 {
				t.Errorf("mode=%q: %+v", c.mode, rt)
			}
		}
	}
	if _, _, err := NewRouter(Config{RoadFactor: 9, AvgSpeedKMH: 30}); err == nil {
		t.Error("noto'g'ri RoadFactor: xato kutilgan")
	}
	for mode, ok := range map[string]bool{"": true, "mock": true, "real": true, " REAL ": true, "auto": false, "on": false} {
		if ValidMode(mode) != ok {
			t.Errorf("ValidMode(%q) != %v", mode, ok)
		}
	}
}
