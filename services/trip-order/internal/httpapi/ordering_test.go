package httpapi_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

const tashkentID = "0a6463ca-a4e0-543c-b0a1-1c12ab814615"

func linesSum(t *testing.T, v any) float64 {
	t.Helper()
	ls, ok := v.([]any)
	if !ok || len(ls) == 0 {
		t.Fatalf("lines yo'q: %v", v)
	}
	var s float64
	for _, it := range ls {
		m := it.(map[string]any)
		if k, _ := m["key"].(string); k == "" {
			t.Fatalf("key yo'q: %v", m)
		}
		s += m["amount"].(float64)
	}
	return s
}

// Narx taklifi: buyurtma aynan ko'rsatilgan narx bilan yaratiladi; quote bir marta, faqat egasi, muddat ichida, o'sha maydonlar bilan.
func TestQuoteThenOrderChargesQuotedPrice(t *testing.T) {
	e := newT(t)
	p := e.passenger()

	q := p.do("POST", "/api/v1/orders/quote", trip(map[string]any{"payment_method": nil}))
	if q.Status != 200 || q.str("quote_id") == "" || q.str("expires_at") == "" {
		t.Fatalf("quote: %d %s", q.Status, q.Raw)
	}
	price := q.Body["price"].(float64)
	if price <= 0 || linesSum(t, q.Body["lines"]) != price {
		t.Fatalf("lines yig'indisi narxga teng emas: %s", q.Raw)
	}

	o := p.order(t, map[string]any{"quote_id": q.str("quote_id")})
	if o.Body["price"] != price || linesSum(t, o.Body["price_lines"]) != price || o.str("quote_id") != q.str("quote_id") {
		t.Fatalf("buyurtma narxi quote'dan farq qiladi: quote=%s order=%s", q.Raw, o.Raw)
	}
	if got := p.do("GET", "/api/v1/orders/"+o.str("id"), nil); got.Body["price"] != price {
		t.Fatalf("GET narxi: %s", got.Raw)
	}

	// ikkinchi marta — rad
	if r := p.do("POST", "/api/v1/orders", trip(map[string]any{"quote_id": q.str("quote_id")})); r.Status != 409 || r.errKey() != "QUOTE_ALREADY_USED" {
		t.Fatalf("qayta ishlatish: %d %s", r.Status, r.Raw)
	}
}

func TestQuoteErrors(t *testing.T) {
	e := newT(t)
	p := e.passenger()
	newQuote := func() string {
		t.Helper()
		r := p.do("POST", "/api/v1/orders/quote", trip(nil))
		if r.Status != 200 {
			t.Fatalf("quote: %d %s", r.Status, r.Raw)
		}
		return r.str("quote_id")
	}

	// maydon o'zgardi (o'rindiqlar soni)
	if r := p.do("POST", "/api/v1/orders", trip(map[string]any{"quote_id": newQuote(), "seats": 3})); r.Status != 409 || r.errKey() != "QUOTE_MISMATCH" {
		t.Fatalf("mismatch: %d %s", r.Status, r.Raw)
	}
	// narxga ta'sir qilmaydigan maydonlar (to'lov, izoh, manzil matni) — mos
	ok := p.do("POST", "/api/v1/orders", trip(map[string]any{"quote_id": newQuote(), "payment_method": "card", "comment": "salom"}))
	if ok.Status != 201 {
		t.Fatalf("to'lov/izoh o'zgarishi rad etilmasligi kerak: %d %s", ok.Status, ok.Raw)
	}
	// begona yo'lovchi
	other := e.passenger()
	if r := other.do("POST", "/api/v1/orders", trip(map[string]any{"quote_id": newQuote()})); r.Status != 404 || r.errKey() != "QUOTE_NOT_FOUND" {
		t.Fatalf("begona quote: %d %s", r.Status, r.Raw)
	}
	// noma'lum
	if r := p.do("POST", "/api/v1/orders", trip(map[string]any{"quote_id": uuid.NewString()})); r.Status != 404 || r.errKey() != "QUOTE_NOT_FOUND" {
		t.Fatalf("noma'lum quote: %d %s", r.Status, r.Raw)
	}
	// muddati o'tgan
	old := newQuote()
	e.advance(11 * time.Minute)
	if r := p.do("POST", "/api/v1/orders", trip(map[string]any{"quote_id": old})); r.Status != 409 || r.errKey() != "QUOTE_EXPIRED" {
		t.Fatalf("eskirgan quote: %d %s", r.Status, r.Raw)
	}
	// quote so'rovida quote_id bo'lmaydi; noto'g'ri format
	if r := p.do("POST", "/api/v1/orders/quote", trip(map[string]any{"quote_id": old})); r.Status != 422 {
		t.Fatalf("quote ichida quote_id: %d %s", r.Status, r.Raw)
	}
	if r := p.do("POST", "/api/v1/orders", trip(map[string]any{"quote_id": "x"})); r.Status != 422 {
		t.Fatalf("quote_id format: %d %s", r.Status, r.Raw)
	}
	// haydovchi quote ololmaydi
	if r := e.driver().do("POST", "/api/v1/orders/quote", trip(nil)); r.Status != 403 {
		t.Fatalf("haydovchi quote: %d %s", r.Status, r.Raw)
	}
}

// Idempotency-Key bilan qayta yuborish quote ishlatilgan bo'lsa ham asl buyurtmani qaytaradi.
func TestQuoteIdempotentRetry(t *testing.T) {
	e := newT(t)
	p := e.passenger()
	q := p.do("POST", "/api/v1/orders/quote", trip(nil))
	key := uuid.NewString()
	body := trip(map[string]any{"quote_id": q.str("quote_id")})
	a := p.with("Idempotency-Key", key).do("POST", "/api/v1/orders", body)
	b := p.with("Idempotency-Key", key).do("POST", "/api/v1/orders", body)
	if a.Status != 201 || b.Status != 201 || a.str("id") != b.str("id") {
		t.Fatalf("idempotent: %d %s / %d %s", a.Status, a.Raw, b.Status, b.Raw)
	}
}

func TestSeatIDsAndRange(t *testing.T) {
	e := newT(t)
	p := e.passenger()

	o := p.order(t, map[string]any{"seats": nil, "seat_ids": []string{"front", "back-left"}})
	ids, _ := o.Body["seat_ids"].([]any)
	if o.Body["seats"] != float64(2) || len(ids) != 2 || ids[0] != "front" {
		t.Fatalf("seat_ids: %s", o.Raw)
	}
	for name, extra := range map[string]map[string]any{
		"duplicate": {"seats": nil, "seat_ids": []string{"front", "front"}},
		"unknown":   {"seats": nil, "seat_ids": []string{"roof"}},
		"mismatch":  {"seats": 3, "seat_ids": []string{"front"}},
		"empty":     {"seats": nil, "seat_ids": []string{}},
		"delivery":  {"type": "delivery", "seats": nil, "vehicle_type": "damas", "seat_ids": []string{"front"}},
		"car_class": {"seats": nil, "car_class": "comfort", "seat_ids": []string{"front"}},
	} {
		if r := p.do("POST", "/api/v1/orders", trip(extra)); r.Status != 422 {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}

	now := e.Clock.Now()
	from, to := now.Add(2*time.Hour).Format(time.RFC3339), now.Add(5*time.Hour).Format(time.RFC3339)
	r := p.order(t, map[string]any{"scheduled_from": from, "scheduled_to": to})
	if r.Body["scheduled_from"] == nil || r.Body["scheduled_to"] == nil || r.Body["scheduled_at"] != nil {
		t.Fatalf("oraliq: %s", r.Raw)
	}
	for name, extra := range map[string]map[string]any{
		"at+range":  {"scheduled_at": from, "scheduled_from": from, "scheduled_to": to},
		"only_from": {"scheduled_from": from},
		"reversed":  {"scheduled_from": to, "scheduled_to": from},
		"short":     {"scheduled_from": from, "scheduled_to": now.Add(2*time.Hour + 5*time.Minute).Format(time.RFC3339)},
		"past":      {"scheduled_from": now.Add(-time.Hour).Format(time.RFC3339), "scheduled_to": to},
		"too_long":  {"scheduled_from": from, "scheduled_to": now.Add(9 * 24 * time.Hour).Format(time.RFC3339)},
	} {
		if r := p.do("POST", "/api/v1/orders", trip(extra)); r.Status != 422 {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	// PATCH: oraliqni aniq vaqtga almashtirish
	upd := p.do("PATCH", "/api/v1/orders/"+r.str("id"), map[string]any{"scheduled_at": from})
	if upd.Status != 200 || upd.Body["scheduled_from"] != nil || upd.Body["scheduled_at"] == nil {
		t.Fatalf("patch vaqt: %d %s", upd.Status, upd.Raw)
	}
}

func TestCatalogEndpoints(t *testing.T) {
	e := newT(t)
	anon := e.as("")

	c := anon.do("GET", "/api/v1/cities", nil)
	data, _ := c.Body["data"].([]any)
	if c.Status != 200 || len(data) < 20 || data[0].(map[string]any)["id"] != tashkentID || data[0].(map[string]any)["name"] != "Toshkent" {
		t.Fatalf("cities: %d %s", c.Status, c.Raw)
	}
	v := anon.do("GET", "/api/v1/delivery/vehicles", nil)
	vs, _ := v.Body["data"].([]any)
	if v.Status != 200 || len(vs) != 6 || vs[0].(map[string]any)["size_label"] != "S" || vs[5].(map[string]any)["vehicle_type"] != "fura" {
		t.Fatalf("vehicles: %s", v.Raw)
	}
	pr := anon.do("GET", "/api/v1/routes/popular", nil)
	rs, _ := pr.Body["data"].([]any)
	if pr.Status != 200 || len(rs) == 0 {
		t.Fatalf("routes: %s", pr.Raw)
	}
	first := rs[0].(map[string]any)
	if first["price_from"].(float64) <= 0 || first["from"].(map[string]any)["city_id"] != tashkentID {
		t.Fatalf("route: %v", first)
	}

	p := e.passenger()
	if r := anon.do("GET", "/api/v1/places/search?q=samar", nil); r.Status != 401 {
		t.Fatalf("token'siz qidiruv: %d", r.Status)
	}
	s := p.do("GET", "/api/v1/places/search?q=SAMAR&near_lat=41.3&near_lng=69.2", nil)
	ps, _ := s.Body["data"].([]any)
	if s.Status != 200 || len(ps) == 0 || ps[0].(map[string]any)["name"] != "Samarqand" || ps[0].(map[string]any)["distance_m"] == nil {
		t.Fatalf("search: %d %s", s.Status, s.Raw)
	}
	if s := p.do("GET", "/api/v1/places/search?q=fargona", nil); len(s.Body["data"].([]any)) == 0 {
		t.Fatalf("apostrofsiz qidiruv: %s", s.Raw)
	}
	if r := p.do("GET", "/api/v1/places/search?q=s", nil); r.Status != 422 {
		t.Fatalf("qisqa q: %d", r.Status)
	}
	rev := p.do("GET", "/api/v1/places/reverse?lat=41.31&lng=69.25", nil)
	if rev.Status != 200 || rev.str("city_id") != tashkentID || rev.Body["distance_m"] == nil {
		t.Fatalf("reverse: %d %s", rev.Status, rev.Raw)
	}
	if r := p.do("GET", "/api/v1/places/reverse?lat=200&lng=1", nil); r.Status != 422 {
		t.Fatalf("reverse validatsiya: %d", r.Status)
	}

	sm := p.do("GET", "/api/v1/orders/seat-map?from_lat=41.3111&from_lng=69.2797&to_lat=39.6542&to_lng=66.9597", nil)
	seats, _ := sm.Body["seats"].([]any)
	if sm.Status != 200 || len(seats) != 4 || seats[0].(map[string]any)["id"] != "front" || seats[0].(map[string]any)["price"].(float64) <= 0 {
		t.Fatalf("seat map: %d %s", sm.Status, sm.Raw)
	}

	// buyurtmada shahar aniqlanadi
	o := p.order(t, nil)
	if o.str("from_city_id") != tashkentID || o.Body["editable"] != true || o.Body["can_cancel"] != true {
		t.Fatalf("order city/verdicts: %s", o.Raw)
	}
}

func TestSavedPlaces(t *testing.T) {
	e := newT(t)
	p, other := e.passenger(), e.passenger()
	c := p.do("POST", "/api/v1/places/saved", map[string]any{"name": "Uy", "lat": 41.3, "lng": 69.2,
		"details": map[string]any{"entrance": "2", "floor": "5", "note": "domofon ishlamaydi"}})
	if c.Status != 201 || c.obj("details")["floor"] != "5" {
		t.Fatalf("create: %d %s", c.Status, c.Raw)
	}
	if r := p.do("POST", "/api/v1/places/saved", map[string]any{"name": "", "lat": 41.3, "lng": 69.2}); r.Status != 422 {
		t.Fatalf("bo'sh nom: %d", r.Status)
	}
	if r := p.do("POST", "/api/v1/places/saved", map[string]any{"name": "X", "lat": 41.3, "lng": 69.2, "extra": 1}); r.Status != 422 {
		t.Fatalf("noma'lum maydon: %d", r.Status)
	}
	if l := other.do("GET", "/api/v1/places/saved", nil); len(l.Body["data"].([]any)) != 0 {
		t.Fatalf("begona ro'yxat: %s", l.Raw)
	}
	if r := other.do("DELETE", "/api/v1/places/saved/"+c.str("id"), nil); r.Status != 403 || r.errParam("reason") != "not_owner" {
		t.Fatalf("begona o'chirish: %d", r.Status)
	}
	if r := p.do("DELETE", "/api/v1/places/saved/"+c.str("id"), nil); r.Status != 204 {
		t.Fatalf("o'chirish: %d %s", r.Status, r.Raw)
	}
	if l := p.do("GET", "/api/v1/places/saved", nil); len(l.Body["data"].([]any)) != 0 {
		t.Fatalf("o'chmagan: %s", l.Raw)
	}
}

// Narxga ta'sir qilmaydigan PATCH (izoh, to'lov, vaqt) tarif o'zgargan bo'lsa ham kelishilgan narxni o'zgartirmaydi;
// o'rindiqlar soni o'zgarsa — qayta hisoblanadi.
func TestPatchKeepsQuotedPrice(t *testing.T) {
	e := newT(t)
	p := e.passenger()
	q := p.do("POST", "/api/v1/orders/quote", trip(nil))
	o := p.order(t, map[string]any{"quote_id": q.str("quote_id")})
	e.Pricer.Factor.Store(200) // tarif ikki barobar qimmatladi
	u := p.do("PATCH", "/api/v1/orders/"+o.str("id"), map[string]any{"comment": "2-podyezd", "payment_method": "card"})
	if u.Status != 200 || u.Body["price"] != q.Body["price"] || linesSum(t, u.Body["price_lines"]) != q.Body["price"].(float64) {
		t.Fatalf("izoh o'zgarishi narxni o'zgartirdi: quote=%v patch=%s", q.Body["price"], u.Raw)
	}
	u = p.do("PATCH", "/api/v1/orders/"+o.str("id"), map[string]any{"seats": 3})
	if u.Status != 200 || u.Body["price"].(float64) <= q.Body["price"].(float64) {
		t.Fatalf("o'rindiqlar o'zgarganda narx qayta hisoblanmadi: %s", u.Raw)
	}
}
