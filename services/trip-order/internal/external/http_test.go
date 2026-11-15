package external

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/domain"
)

const testToken = "test-internal-token-0123456789"

// fakeServer — bitta ichki endpointni taqlid qiladi: metod, yo'l, token va tanani (DisallowUnknownFields bilan —
// real servislar kabi) tekshiradi, keyin berilgan status/javobni qaytaradi.
func fakeServer[Req any](t *testing.T, path string, status int, resp any, got *Req) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != path {
			t.Errorf("so'rov: %s %s, kutilgan POST %s", r.Method, r.URL.Path, path)
		}
		if h := r.Header.Get(InternalTokenHeader); h != testToken {
			t.Errorf("%s = %q", InternalTokenHeader, h)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(got); err != nil {
			t.Errorf("tana: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hc() *http.Client { return &http.Client{Timeout: 2 * time.Second} }

func errBody(key string) map[string]any {
	return map[string]any{"error": map[string]any{"key": key, "params": map[string]any{}}}
}

func wantStatusErr(t *testing.T, err error, status int, key string) {
	t.Helper()
	var se *StatusError
	if !errors.As(err, &se) || se.Status != status || se.Key != key {
		t.Fatalf("xato = %v, kutilgan HTTP %d %s", err, status, key)
	}
}

// ---------------------------------------------------------------- Pricing

type pricingWire struct {
	Type        string     `json:"type"`
	Seats       *int       `json:"seats"`
	CarClass    *string    `json:"car_class"`
	VehicleType *string    `json:"vehicle_type"`
	From        *pointJSON `json:"from"`
	To          *pointJSON `json:"to"`
}

func TestHTTPPricerQuote(t *testing.T) {
	var got pricingWire
	srv := fakeServer(t, "/internal/pricing/calculate", 200,
		map[string]any{"price": 1_250_000, "commission": 125_000, "distance_m": 5400, "duration_s": 675, "tariff_version": 3}, &got)

	from, to := p(41.3111, 69.2797), p(41.2995, 69.2401)
	sched := time.Now().Add(time.Hour)
	q, err := NewHTTPPricer(srv.URL+"/", testToken, hc()).Quote(context.Background(),
		QuoteRequest{Type: domain.TypePassenger, Seats: ip(2), From: from, To: to, ScheduledAt: &sched})
	if err != nil {
		t.Fatal(err)
	}
	if q != (Quote{Price: 1_250_000, Commission: 125_000, DistanceM: 5400, DurationS: 675}) {
		t.Errorf("quote = %+v", q)
	}
	if got.Type != "passenger" || got.Seats == nil || *got.Seats != 2 || got.CarClass != nil || got.VehicleType != nil ||
		got.From == nil || *got.From != (pointJSON{41.3111, 69.2797}) || got.To == nil || *got.To != (pointJSON{41.2995, 69.2401}) {
		t.Errorf("so'rov = %+v", got)
	}
}

func TestHTTPPricerQuoteDelivery(t *testing.T) {
	var got pricingWire
	srv := fakeServer(t, "/internal/pricing/calculate", 200,
		map[string]any{"price": 3_000_000, "commission": 300_000, "distance_m": 10000, "duration_s": 1250}, &got)
	vt := domain.Labo
	if _, err := NewHTTPPricer(srv.URL, testToken, hc()).Quote(context.Background(),
		QuoteRequest{Type: domain.TypeDelivery, VehicleType: &vt, From: p(41, 69), To: p(41.1, 69.1)}); err != nil {
		t.Fatal(err)
	}
	if got.Type != "delivery" || got.VehicleType == nil || *got.VehicleType != "labo" || got.Seats != nil || got.CarClass != nil {
		t.Errorf("so'rov = %+v", got)
	}
}

func TestHTTPPricerErrors(t *testing.T) {
	var got pricingWire
	req := QuoteRequest{Type: domain.TypePassenger, Seats: ip(1), From: p(41, 69), To: p(41.1, 69.1)}

	srv := fakeServer(t, "/internal/pricing/calculate", 422, errBody("VALIDATION_ERROR"), &got)
	_, err := NewHTTPPricer(srv.URL, testToken, hc()).Quote(context.Background(), req)
	wantStatusErr(t, err, 422, "VALIDATION_ERROR")

	srv = fakeServer(t, "/internal/pricing/calculate", 403, errBody("FORBIDDEN"), &got)
	_, err = NewHTTPPricer(srv.URL, testToken, hc()).Quote(context.Background(), req)
	wantStatusErr(t, err, 403, "FORBIDDEN")

	// Ma'nosiz narx buyurtmaga yozilmasligi kerak.
	for _, bad := range []map[string]any{
		{"price": 0, "commission": 0, "distance_m": 1, "duration_s": 1},
		{"price": 100, "commission": 200, "distance_m": 1, "duration_s": 1},
		{"price": 100, "commission": -1, "distance_m": 1, "duration_s": 1},
	} {
		srv = fakeServer(t, "/internal/pricing/calculate", 200, bad, &got)
		if _, err := NewHTTPPricer(srv.URL, testToken, hc()).Quote(context.Background(), req); err == nil {
			t.Errorf("%v: xato kutilgan edi", bad)
		}
	}
}

func TestHTTPPricerFinalizeKeepsQuote(t *testing.T) {
	q := Quote{Price: 900_000, Commission: 90_000, DistanceM: 4200, DurationS: 500}
	f, err := HTTPPricer{}.Finalize(context.Background(), FinalizeRequest{Quoted: q})
	if err != nil {
		t.Fatal(err)
	}
	if f != (Final{Price: q.Price, Commission: q.Commission, DistanceM: q.DistanceM}) {
		t.Errorf("final = %+v", f)
	}
}

// ---------------------------------------------------------------- Matching

type matchingWire struct {
	Type        string     `json:"type"`
	Seats       *int       `json:"seats"`
	CarClass    *string    `json:"car_class"`
	VehicleType *string    `json:"vehicle_type"`
	From        *pointJSON `json:"from"`
	RadiusM     *float64   `json:"radius_m"`
	Limit       *int       `json:"limit"`
	OrderID     *string    `json:"order_id"`
	ExpiresAt   *string    `json:"expires_at"`
}

func TestHTTPMatcherFindDrivers(t *testing.T) {
	d1, d2 := uuid.New(), uuid.New()
	var got matchingWire
	srv := fakeServer(t, "/internal/matching/find", 200, map[string]any{"drivers": []map[string]any{
		{"driver_id": d1, "distance_m": 300, "location": map[string]any{"lat": 41.3, "lng": 69.2}, "pickup_eta_s": 36},
		{"driver_id": d2, "distance_m": 900, "location": map[string]any{"lat": 41.31, "lng": 69.21}, "pickup_eta_s": 108},
		{"driver_id": d1, "distance_m": 950, "location": map[string]any{"lat": 41.31, "lng": 69.21}, "pickup_eta_s": 110}, // takror
	}}, &got)

	cc := domain.Comfort
	o := domain.Order{ID: uuid.New(), Type: domain.TypePassenger, CarClass: &cc, From: p(41.3, 69.25), To: p(41.2, 69.1)}
	cands, err := NewHTTPMatcher(srv.URL, testToken, hc()).FindDrivers(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	want := []Candidate{{DriverID: d1, PickupETASeconds: 36}, {DriverID: d2, PickupETASeconds: 108}}
	if len(cands) != len(want) || cands[0] != want[0] || cands[1] != want[1] {
		t.Errorf("nomzodlar = %+v", cands)
	}
	if got.Type != "passenger" || got.CarClass == nil || *got.CarClass != "comfort" || got.Seats != nil ||
		got.From == nil || *got.From != (pointJSON{41.3, 69.25}) || got.Limit == nil || *got.Limit != matchingLimit {
		t.Errorf("so'rov = %+v", got)
	}
	// Takliflarni trip-order o'zi yaratadi: order_id yuborilsa matching-geo boshqa offer_id bilan taklif yaratardi.
	if got.OrderID != nil || got.ExpiresAt != nil {
		t.Errorf("order_id/expires_at yuborilmasligi kerak: %+v", got)
	}
}

func TestHTTPMatcherEmptyAndError(t *testing.T) {
	var got matchingWire
	o := domain.Order{Type: domain.TypePassenger, Seats: ip(1), From: p(41, 69)}

	srv := fakeServer(t, "/internal/matching/find", 200, map[string]any{"drivers": []any{}}, &got)
	cands, err := NewHTTPMatcher(srv.URL, testToken, hc()).FindDrivers(context.Background(), o)
	if err != nil || len(cands) != 0 {
		t.Fatalf("cands=%v err=%v", cands, err)
	}

	srv = fakeServer(t, "/internal/matching/find", 500, errBody("INTERNAL_ERROR"), &got)
	_, err = NewHTTPMatcher(srv.URL, testToken, hc()).FindDrivers(context.Background(), o)
	wantStatusErr(t, err, 500, "INTERNAL_ERROR")
}

// ---------------------------------------------------------------- Wallet

type walletWire struct {
	DriverID *uuid.UUID `json:"driver_id"`
}

func TestHTTPWalletCheckDriver(t *testing.T) {
	driver := uuid.New()
	for _, tc := range []struct {
		allowed bool
		want    WalletStatus
	}{
		{true, WalletStatus{LimitReached: false, Limit: -5_000_000}},
		{false, WalletStatus{LimitReached: true, Limit: -5_000_000}},
	} {
		var got walletWire
		srv := fakeServer(t, "/internal/wallet/check-limit", 200,
			map[string]any{"allowed": tc.allowed, "balance": -5_100_000, "debt_status": "at_limit", "limit": -5_000_000}, &got)
		ws, err := NewHTTPWallet(srv.URL, testToken, hc()).CheckDriver(context.Background(), driver)
		if err != nil {
			t.Fatal(err)
		}
		if ws != tc.want {
			t.Errorf("allowed=%v: status = %+v", tc.allowed, ws)
		}
		if got.DriverID == nil || *got.DriverID != driver {
			t.Errorf("so'rov = %+v", got)
		}
	}
}

func TestHTTPWalletErrors(t *testing.T) {
	var got walletWire
	srv := fakeServer(t, "/internal/wallet/check-limit", 403, errBody("FORBIDDEN"), &got)
	_, err := NewHTTPWallet(srv.URL, testToken, hc()).CheckDriver(context.Background(), uuid.New())
	wantStatusErr(t, err, 403, "FORBIDDEN")

	// allowed yo'q — "ruxsat" deb taxmin qilinmaydi.
	srv = fakeServer(t, "/internal/wallet/check-limit", 200, map[string]any{"limit": -1}, &got)
	if _, err := NewHTTPWallet(srv.URL, testToken, hc()).CheckDriver(context.Background(), uuid.New()); err == nil {
		t.Error("allowed yo'q: xato kutilgan edi")
	}
}

// ---------------------------------------------------------------- umumiy

func TestHTTPContextCancelAndUnreachable(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(block); srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := NewHTTPWallet(srv.URL, testToken, hc()).CheckDriver(ctx, uuid.New()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("bekor qilingan kontekst: %v", err)
	}

	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	if _, err := NewHTTPPricer(url, testToken, hc()).Quote(context.Background(),
		QuoteRequest{Type: domain.TypePassenger, Seats: ip(1), From: p(41, 69), To: p(41.1, 69.1)}); err == nil {
		t.Error("ishlamayotgan servis: xato kutilgan edi")
	}
}

func TestNewSelectsPerService(t *testing.T) {
	mock := RedisMatcher{}

	c := New(Endpoints{Token: testToken, Timeout: time.Second}, mock)
	if _, ok := c.Pricer.(MockPricer); !ok {
		t.Errorf("pricer = %T", c.Pricer)
	}
	if _, ok := c.Wallet.(AllowAllWallet); !ok {
		t.Errorf("wallet = %T", c.Wallet)
	}
	if _, ok := c.Matcher.(RedisMatcher); !ok {
		t.Errorf("matcher = %T", c.Matcher)
	}

	c = New(Endpoints{PricingURL: "http://pricing:8014", WalletURL: " ", Token: testToken, Timeout: time.Second}, mock)
	if _, ok := c.Pricer.(HTTPPricer); !ok {
		t.Errorf("pricer = %T", c.Pricer)
	}
	if _, ok := c.Wallet.(AllowAllWallet); !ok {
		t.Errorf("bo'sh WALLET_URL: wallet = %T", c.Wallet)
	}
	if _, ok := c.Matcher.(RedisMatcher); !ok {
		t.Errorf("matcher = %T", c.Matcher)
	}
	if !c.Real["pricing"] || c.Real["wallet"] || c.Real["matching"] {
		t.Errorf("real = %v", c.Real)
	}

	c = New(Endpoints{PricingURL: "http://p", MatchingURL: "http://m", WalletURL: "http://w", Token: testToken, Timeout: time.Second}, mock)
	if _, ok := c.Matcher.(HTTPMatcher); !ok {
		t.Errorf("matcher = %T", c.Matcher)
	}
	if _, ok := c.Wallet.(HTTPWallet); !ok {
		t.Errorf("wallet = %T", c.Wallet)
	}
}
