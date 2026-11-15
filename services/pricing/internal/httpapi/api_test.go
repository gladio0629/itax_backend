package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/izgo/pricing/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
}

func (r resp) errKey() string {
	e, _ := r.Body["error"].(map[string]any)
	k, _ := e["key"].(string)
	return k
}

func call(t *testing.T, e *testenv.Env, method, path, token string, body any) resp {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewBufferString(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, e.Server.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: raw}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.Body); err != nil {
			t.Fatalf("JSON emas (%d): %q", res.StatusCode, raw)
		}
	}
	return out
}

func calc(t *testing.T, e *testenv.Env, body map[string]any) resp {
	t.Helper()
	return call(t, e, "POST", "/internal/pricing/calculate", testenv.Token, body)
}

// Toshkent ichida ~3.55 km to'g'ri chiziq -> 4615 m yo'l -> 554 s.
func req(extra map[string]any) map[string]any {
	b := map[string]any{
		"type": "passenger", "seats": 2,
		"from": map[string]any{"lat": 41.3111, "lng": 69.2797},
		"to":   map[string]any{"lat": 41.2995, "lng": 69.2401},
	}
	for k, v := range extra {
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
	}
	return b
}

func TestCalculatePassenger(t *testing.T) {
	e := testenv.New(t)
	// boshlang'ich tarif: base 400 000 + 4615 m × 120 000 tiyin/km × seats (553 800 × seats); yaxlitlash va min_fare yo'q
	for seats, want := range map[int]float64{1: 953_800, 2: 1_507_600, 3: 2_061_400, 4: 2_615_200} {
		r := calc(t, e, req(map[string]any{"seats": seats}))
		if r.Status != 200 || r.Body["price"] != want || r.Body["distance_m"] != float64(4615) || r.Body["duration_s"] != float64(554) ||
			r.Body["commission"] != want/10 || r.Body["tariff_version"] != float64(1) {
			t.Errorf("seats=%d: %d %s", seats, r.Status, r.Raw)
		}
	}
	// butun mashina: 400 000 + 553.8 × omil‰ (economy 4000, comfort 5000, business 6400, minivan 6000)
	for class, want := range map[string]float64{"economy": 2_615_200, "comfort": 3_169_000, "business": 3_944_320, "minivan": 3_722_800} {
		r := calc(t, e, req(map[string]any{"seats": nil, "car_class": class}))
		if r.Status != 200 || r.Body["price"] != want {
			t.Errorf("%s: %d %s (kutilgan %v)", class, r.Status, r.Raw, want)
		}
	}
}

func TestCalculateDelivery(t *testing.T) {
	e := testenv.New(t)
	// delivery: 400 000 + 553 800 × m  (spark=1.0, damas=1.3, labo=1.5, isuzu=2.0, isuzu2=2.5, fura=3.0)
	want := map[string]float64{
		"spark": 953_800, "damas": 1_119_940, "labo": 1_230_700, "isuzu": 1_507_600, "isuzu2": 1_784_500, "fura": 2_061_400,
	}
	var last float64
	for _, v := range []string{"spark", "damas", "labo", "isuzu", "isuzu2", "fura"} {
		r := calc(t, e, req(map[string]any{"type": "delivery", "seats": nil, "vehicle_type": v}))
		if r.Status != 200 || r.Body["price"] != want[v] {
			t.Errorf("%s: %d %s (kutilgan %v)", v, r.Status, r.Raw, want[v])
		}
		if p := r.Body["price"].(float64); p <= last {
			t.Errorf("%s narxi oldingisidan qimmat bo'lishi kerak", v)
		} else {
			last = p
		}
	}
}

// min_fare va rounding_step tarifda yoqilganda amal qiladi (boshlang'ich tarifda o'chiq).
func TestMinFareAndRoundingFromTariff(t *testing.T) {
	e := testenv.New(t)
	ctx := context.Background()
	short := req(map[string]any{"seats": 1, "to": map[string]any{"lat": 41.3112, "lng": 69.2798}})
	if r := calc(t, e, short); r.Status != 200 || r.Body["price"].(float64) >= 700_000 {
		t.Fatalf("boshlang'ich tarifda min_fare yo'q: %s", r.Raw)
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE tariffs SET min_fare = 700000, rounding_step = 10000`); err != nil {
		t.Fatal(err)
	}
	if err := e.Cache.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	if r := calc(t, e, short); r.Status != 200 || r.Body["price"] != float64(700_000) {
		t.Errorf("min_fare: %s", r.Raw)
	}
	if r := calc(t, e, req(nil)); r.Body["price"] != float64(1_510_000) { // 1 507 600 -> 1 510 000
		t.Errorf("yaxlitlash: %s", r.Raw)
	}
}

func TestInternalTokenRequired(t *testing.T) {
	e := testenv.New(t)
	for name, tok := range map[string]string{"yo'q": "", "noto'g'ri": "wrong-token-0123456789", "prefiks": testenv.Token[:10], "uzun": testenv.Token + "x"} {
		r := call(t, e, "POST", "/internal/pricing/calculate", tok, req(nil))
		if r.Status != 403 || r.errKey() != "FORBIDDEN" {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	// almashtirish davrida ikkala token ham ishlaydi
	if r := call(t, e, "POST", "/internal/pricing/calculate", "old-rotating-token-9876543210", req(nil)); r.Status != 200 {
		t.Errorf("eski token: %d", r.Status)
	}
	// ruxsat yo'q so'rov hisoblash/validatsiyagacha bormaydi; noma'lum yo'l ham tokenni talab qiladi
	if r := call(t, e, "GET", "/internal/pricing/other", "", nil); r.Status != 403 {
		t.Errorf("tokensiz noma'lum yo'l: %d", r.Status)
	}
	if r := call(t, e, "GET", "/internal/pricing/other", testenv.Token, nil); r.Status != 404 {
		t.Errorf("tokenli noma'lum yo'l: %d", r.Status)
	}
	// healthz tokensiz ochiq (docker healthcheck)
	res, err := http.Get(e.Server.URL + "/healthz")
	if err != nil || res.StatusCode != 200 {
		t.Errorf("healthz: %v %v", res, err)
	}
}

func TestCalculateValidation(t *testing.T) {
	e := testenv.New(t)
	bad := map[string]map[string]any{
		"type yo'q":             req(map[string]any{"type": nil}),
		"type noto'g'ri":        req(map[string]any{"type": "taxi"}),
		"seats/class yo'q":      req(map[string]any{"seats": nil}),
		"seats 0":               req(map[string]any{"seats": 0}),
		"seats 5":               req(map[string]any{"seats": 5}),
		"seats + car_class":     req(map[string]any{"car_class": "economy"}),
		"car_class noto'g'ri":   req(map[string]any{"seats": nil, "car_class": "vip"}),
		"passenger + vehicle":   req(map[string]any{"vehicle_type": "labo"}),
		"delivery vehicle yo'q": req(map[string]any{"type": "delivery", "seats": nil}),
		"delivery + seats":      req(map[string]any{"type": "delivery", "vehicle_type": "labo"}),
		"vehicle noto'g'ri":     req(map[string]any{"type": "delivery", "seats": nil, "vehicle_type": "bus"}),
		"from yo'q":             req(map[string]any{"from": nil}),
		"lat > 90":              req(map[string]any{"to": map[string]any{"lat": 91, "lng": 69}}),
		"lng < -180":            req(map[string]any{"to": map[string]any{"lat": 41, "lng": -181}}),
		"lng yo'q":              req(map[string]any{"to": map[string]any{"lat": 41}}),
		"narx yuborish":         req(map[string]any{"price": 1}),
		"masofa yuborish":       req(map[string]any{"distance_m": 1}),
	}
	for name, body := range bad {
		if r := calc(t, e, body); r.Status != 422 || r.errKey() != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	for name, raw := range map[string]string{"buzuq JSON": `{"type":`, "ikki qiymat": `{"type":"passenger"} {}`, "massiv": `[]`, "bo'sh": ``} {
		if r := call(t, e, "POST", "/internal/pricing/calculate", testenv.Token, raw); r.Status != 422 {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	// bir xil nuqta — xato emas, minimal narx
	same := map[string]any{"lat": 41.3, "lng": 69.2}
	if r := calc(t, e, req(map[string]any{"from": same, "to": same})); r.Status != 200 || r.Body["distance_m"] != float64(0) {
		t.Errorf("bir xil nuqta: %d %s", r.Status, r.Raw)
	}
}

// Tarif Redis'da 5 daqiqa keshlanadi; o'zgarish TTL dan keyin (yoki Invalidate'dan keyin) ko'rinadi.
func TestTariffCachingAndChange(t *testing.T) {
	e := testenv.New(t)
	ctx := context.Background()

	before := calc(t, e, req(nil))
	if before.Status != 200 || before.Body["price"] != float64(1_507_600) {
		t.Fatalf("%d %s", before.Status, before.Raw)
	}
	ttl := e.MR.TTL("pricing:tariff:active")
	if ttl <= 4*time.Minute || ttl > 5*time.Minute {
		t.Fatalf("kesh TTL = %v, 5 daqiqa bo'lishi kerak", ttl)
	}

	// Backoffice yangi tarifni faollashtiradi: per_km_rate ikki baravar (v2)
	activate := func() {
		tx, err := e.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		for _, q := range []string{
			`UPDATE tariffs SET active=false WHERE active`,
			`INSERT INTO tariffs (id, version, active, base_fare, per_km_rate, seat_multiplier, car_class_multipliers, vehicle_multipliers,
				fuel_surcharge, min_fare, rounding_step, commission_bps, activated_at)
			 SELECT gen_random_uuid(), 2, true, base_fare, per_km_rate*2, seat_multiplier, car_class_multipliers, vehicle_multipliers,
				fuel_surcharge, min_fare, rounding_step, commission_bps, now() FROM tariffs WHERE version=1`,
		} {
			if _, err := tx.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	activate()

	// kesh amal qiladi: hali eski narx
	if r := calc(t, e, req(nil)); r.Body["price"] != float64(1_507_600) || r.Body["tariff_version"] != float64(1) {
		t.Fatalf("kesh amal qilishi kerak: %s", r.Raw)
	}
	// TTL tugagach yangi tarif: 400 000 + 4615 × 240 000/1000 × 2 = 2 615 200
	e.MR.FastForward(5*time.Minute + time.Second)
	if r := calc(t, e, req(nil)); r.Body["price"] != float64(2_615_200) || r.Body["tariff_version"] != float64(2) {
		t.Fatalf("TTL dan keyin: %s", r.Raw)
	}
	// Invalidate (keyingi NATS hodisasi o'rniga): darhol ko'rinadi
	if _, err := e.Pool.Exec(ctx, `UPDATE tariffs SET base_fare = 0 WHERE active`); err != nil {
		t.Fatal(err)
	}
	if err := e.Cache.Invalidate(ctx); err != nil {
		t.Fatal(err)
	}
	if r := calc(t, e, req(nil)); r.Body["price"] != float64(2_215_200) { // base 0 + 2 215 200
		t.Fatalf("Invalidate'dan keyin: %s", r.Raw)
	}
}

// Redis ishlamasa yoki kesh buzilsa ham narx bazadan hisoblanadi.
func TestCacheFailureFallsBackToDB(t *testing.T) {
	e := testenv.New(t)
	e.MR.Set("pricing:tariff:active", "{buzuq json")
	if r := calc(t, e, req(nil)); r.Status != 200 || r.Body["price"] != float64(1_507_600) {
		t.Fatalf("buzuq kesh: %d %s", r.Status, r.Raw)
	}
	// keshdagi tarif Validate'dan o'tmasa ham e'tiborsiz
	e.MR.Set("pricing:tariff:active", `{"version":99,"base_fare":-5}`)
	if r := calc(t, e, req(nil)); r.Status != 200 || r.Body["tariff_version"] != float64(1) {
		t.Fatalf("yaroqsiz kesh: %d %s", r.Status, r.Raw)
	}
	e.MR.Close() // Redis butunlay o'chdi
	if r := calc(t, e, req(nil)); r.Status != 200 || r.Body["price"] != float64(1_507_600) {
		t.Fatalf("Redis yo'q: %d %s", r.Status, r.Raw)
	}
}

func TestNoActiveTariffIsInternalError(t *testing.T) {
	e := testenv.New(t)
	if _, err := e.Pool.Exec(context.Background(), `UPDATE tariffs SET active=false`); err != nil {
		t.Fatal(err)
	}
	e.MR.FlushAll()
	r := calc(t, e, req(nil))
	if r.Status != 500 || r.errKey() != "INTERNAL_ERROR" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}
