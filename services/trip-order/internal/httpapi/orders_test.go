package httpapi_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCreateOrderPassenger(t *testing.T) {
	e := newT(t)
	p := e.passenger()

	r := p.do("POST", "/api/v1/orders", trip(nil))
	if r.Status != 201 || r.str("status") != "searching" || r.str("type") != "passenger" || r.Body["seats"] != float64(2) {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	price, _ := r.Body["price"].(float64)
	if price <= 0 || int64(price)%10000 != 0 || r.Body["distance_m"].(float64) <= 0 || r.Body["driver"] != nil || r.Body["trip_id"] != nil {
		t.Fatalf("narx/masofa/driver: %s", r.Raw)
	}
	if _, err := uuid.Parse(r.str("id")); err != nil {
		t.Fatalf("id UUID emas: %s", r.str("id"))
	}

	// order.created hodisasi kontrakt bo'yicha
	evs := e.Outbox()
	if len(evs) != 1 || evs[0].Subject != "izgo.order.created.v1" {
		t.Fatalf("outbox: %+v", evs)
	}
	var ev struct {
		Event, TraceID string
		Version        int
		EventID        string `json:"event_id"`
		Payload        map[string]any
	}
	if err := json.Unmarshal(evs[0].Payload, &ev); err != nil {
		t.Fatal(err)
	}
	pl := ev.Payload
	if ev.Event != "order.created" || ev.Version != 1 || ev.EventID != evs[0].ID.String() ||
		pl["order_id"] != r.str("id") || pl["passenger_id"] != p.ID.String() || pl["type"] != "passenger" || pl["seats"] != float64(2) ||
		pl["price"] != price || pl["payment_method"] != "cash" || pl["created_at"] == nil || pl["from"] == nil || pl["to"] == nil {
		t.Fatalf("hodisa: %s", evs[0].Payload)
	}
	if _, has := pl["car_class"]; has {
		t.Fatal("seats buyurtmasida car_class bo'lmasligi kerak")
	}

	// car_class bilan (butun mashina) va payme/click
	for _, pm := range []string{"payme", "click", "card", "wallet"} {
		o := p.do("POST", "/api/v1/orders", trip(map[string]any{"seats": nil, "car_class": "comfort", "payment_method": pm}))
		if o.Status != 201 || o.str("car_class") != "comfort" || o.Body["seats"] != nil {
			t.Fatalf("%s: %d %s", pm, o.Status, o.Raw)
		}
		p.do("POST", "/api/v1/orders/"+o.str("id")+"/cancel", nil)
	}
}

func TestCreateOrderDelivery(t *testing.T) {
	e := newT(t)
	p := e.passenger()
	prices := map[string]float64{}
	for _, v := range []string{"spark", "damas", "labo", "isuzu", "isuzu2", "fura"} {
		r := p.do("POST", "/api/v1/orders", trip(map[string]any{"type": "delivery", "seats": nil, "vehicle_type": v}))
		if r.Status != 201 || r.str("vehicle_type") != v || r.Body["seats"] != nil {
			t.Fatalf("%s: %d %s", v, r.Status, r.Raw)
		}
		prices[v] = r.Body["price"].(float64)
		p.do("POST", "/api/v1/orders/"+r.str("id")+"/cancel", nil)
	}
	if !(prices["spark"] < prices["damas"] && prices["damas"] < prices["labo"] && prices["labo"] < prices["isuzu"] &&
		prices["isuzu"] < prices["isuzu2"] && prices["isuzu2"] < prices["fura"]) {
		t.Fatalf("narxlar mashina turiga qarab oshishi kerak: %v", prices)
	}
	ev := e.Outbox()[0]
	var m struct{ Payload map[string]any }
	_ = json.Unmarshal(ev.Payload, &m)
	if m.Payload["vehicle_type"] != "spark" || m.Payload["type"] != "delivery" {
		t.Fatalf("delivery hodisasi: %s", ev.Payload)
	}
}

func TestCreateOrderValidation(t *testing.T) {
	e := newT(t)
	p := e.passenger()
	in := func(h time.Duration) string { return e.Clock.Now().Add(h).Format(time.RFC3339) }
	cases := map[string]map[string]any{
		"klient narx yuboradi":        trip(map[string]any{"price": 1}),
		"klient status yuboradi":      trip(map[string]any{"status": "accepted"}),
		"type yo'q":                   trip(map[string]any{"type": nil}),
		"type noto'g'ri":              trip(map[string]any{"type": "taxi"}),
		"passenger seats/class yo'q":  trip(map[string]any{"seats": nil}),
		"seats va car_class ikkalasi": trip(map[string]any{"car_class": "economy"}),
		"seats 0":                     trip(map[string]any{"seats": 0}),
		"seats 5":                     trip(map[string]any{"seats": 5}),
		"car_class noto'g'ri":         trip(map[string]any{"seats": nil, "car_class": "vip"}),
		"passenger + vehicle_type":    trip(map[string]any{"vehicle_type": "labo"}),
		"delivery vehicle_type yo'q":  trip(map[string]any{"type": "delivery", "seats": nil}),
		"delivery + seats":            trip(map[string]any{"type": "delivery", "vehicle_type": "labo"}),
		"delivery noto'g'ri mashina":  trip(map[string]any{"type": "delivery", "seats": nil, "vehicle_type": "bus"}),
		"from yo'q":                   trip(map[string]any{"from": nil}),
		"lat chegaradan tashqari":     trip(map[string]any{"to": map[string]any{"lat": 91, "lng": 69}}),
		"lng yo'q":                    trip(map[string]any{"to": map[string]any{"lat": 41}}),
		"from == to":                  trip(map[string]any{"to": map[string]any{"lat": 41.3111, "lng": 69.2797}}),
		"to'lov usuli yo'q":           trip(map[string]any{"payment_method": nil}),
		"to'lov usuli noto'g'ri":      trip(map[string]any{"payment_method": "bitcoin"}),
		"scheduled o'tmishda":         trip(map[string]any{"scheduled_at": in(-time.Hour)}),
		"scheduled juda uzoq":         trip(map[string]any{"scheduled_at": in(24 * 365 * time.Hour)}),
		"scheduled formati":           trip(map[string]any{"scheduled_at": "ertaga"}),
		"izoh uzun":                   trip(map[string]any{"comment": string(make([]rune, 201))}),
	}
	for name, body := range cases {
		if body["comment"] != nil {
			body["comment"] = fmt.Sprintf("%0201d", 0)
		}
		r := p.do("POST", "/api/v1/orders", body)
		if r.Status != 422 || r.errKey() != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	if r := p.do("POST", "/api/v1/orders", `{"type":`); r.Status != 422 {
		t.Errorf("buzuq JSON: %d", r.Status)
	}
	// to'g'ri rejalashtirilgan buyurtma
	if r := p.do("POST", "/api/v1/orders", trip(map[string]any{"scheduled_at": in(3 * time.Hour), "comment": "eshik oldida"})); r.Status != 201 || r.Body["scheduled_at"] == nil || r.str("comment") != "eshik oldida" {
		t.Errorf("scheduled: %d %s", r.Status, r.Raw)
	}
	if n := len(e.Outbox()); n != 1 {
		t.Errorf("rad etilgan so'rovlar hodisa chiqarmasligi kerak: %d", n)
	}
}

func TestAuthAndRoles(t *testing.T) {
	e := newT(t)
	p, d := e.passenger(), e.driver()
	anon := e.as("")

	if r := anon.do("POST", "/api/v1/orders", trip(nil)); r.Status != 401 || r.errKey() != "AUTH_TOKEN_EXPIRED" {
		t.Errorf("tokensiz: %d %s", r.Status, r.Raw)
	}
	if r := e.as("abc.def.ghi").do("GET", "/api/v1/orders/history", nil); r.Status != 401 {
		t.Errorf("axlat token: %d", r.Status)
	}
	if r := e.as(e.TokenExpired("passenger", uuid.New())).do("GET", "/api/v1/orders/history", nil); r.Status != 401 {
		t.Errorf("muddati o'tgan: %d", r.Status)
	}
	if r := e.as(e.TokenAud("passenger", uuid.New(), "admin")).do("GET", "/api/v1/orders/history", nil); r.Status != 401 {
		t.Errorf("admin audience: %d", r.Status)
	}
	if r := e.as(e.TokenAud("driver", uuid.New(), "client")).do("GET", "/api/v1/trips/offers", nil); r.Status != 401 {
		t.Errorf("rol/audience mos emas: %d", r.Status)
	}
	// rol bo'yicha
	if r := d.do("POST", "/api/v1/orders", trip(nil)); r.Status != 403 || r.errParam("reason") != "role" {
		t.Errorf("haydovchi buyurtma yarata olmaydi: %d %s", r.Status, r.Raw)
	}
	if r := p.do("GET", "/api/v1/trips/offers", nil); r.Status != 403 {
		t.Errorf("yo'lovchi takliflarni ko'ra olmaydi: %d", r.Status)
	}
	// X-App audience'ga mos kelishi kerak
	if r := p.with("X-App", "driver").do("GET", "/api/v1/orders/history", nil); r.Status != 403 || r.errParam("reason") != "app_mismatch" {
		t.Errorf("X-App mos emas: %d %s", r.Status, r.Raw)
	}
	if r := p.with("X-App", "client").do("GET", "/api/v1/orders/history", nil); r.Status != 200 {
		t.Errorf("X-App mos: %d", r.Status)
	}
	// Identity logout qilgan sessiya (Redis blacklist) darhol o'chadi
	sid := uuid.NewString()
	pid := uuid.New()
	tok := e.TokenSID("passenger", pid, sid)
	if r := e.as(tok).do("GET", "/api/v1/orders/history", nil); r.Status != 200 {
		t.Fatalf("blacklistdan oldin: %d", r.Status)
	}
	e.MR.Set("bl:sid:"+sid, "1")
	if r := e.as(tok).do("GET", "/api/v1/orders/history", nil); r.Status != 401 {
		t.Errorf("blacklistdagi sessiya: %d", r.Status)
	}
}

func TestGetOrderOwnership(t *testing.T) {
	e := newT(t)
	p1, p2, d1, d2 := e.passenger(), e.passenger(), e.driver(), e.driver()
	e.AddDrivers(d1.ID)
	o := p1.order(t, nil)
	id := o.str("id")

	if r := p1.do("GET", "/api/v1/orders/"+id, nil); r.Status != 200 || r.str("id") != id {
		t.Fatalf("egasi: %d %s", r.Status, r.Raw)
	}
	for name, u := range map[string]user{"boshqa yo'lovchi": p2, "taklif olgan, lekin biriktirilmagan haydovchi": d1, "begona haydovchi": d2} {
		if r := u.do("GET", "/api/v1/orders/"+id, nil); r.Status != 404 || r.errKey() != "ORDER_NOT_FOUND" {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	// biriktirilgach haydovchi ham ko'radi (F-10), begonasi yo'q
	if r := d1.do("POST", "/api/v1/trips/offers/"+d1.offerFor(t, id)+"/accept", nil); r.Status != 200 {
		t.Fatal(r.Status, string(r.Raw))
	}
	if r := d1.do("GET", "/api/v1/orders/"+id, nil); r.Status != 200 || r.obj("driver")["id"] != d1.ID.String() {
		t.Errorf("biriktirilgan haydovchi: %d %s", r.Status, r.Raw)
	}
	if r := d2.do("GET", "/api/v1/orders/"+id, nil); r.Status != 404 {
		t.Errorf("begona haydovchi: %d", r.Status)
	}
	for _, bad := range []string{"not-a-uuid", uuid.NewString()} {
		if r := p1.do("GET", "/api/v1/orders/"+bad, nil); r.Status != 404 || r.errKey() != "ORDER_NOT_FOUND" {
			t.Errorf("%s: %d %s", bad, r.Status, r.Raw)
		}
	}
}

func TestPatchOrder(t *testing.T) {
	e := newT(t)
	p, other, d := e.passenger(), e.passenger(), e.driver()
	e.AddDrivers(d.ID)
	o := p.order(t, map[string]any{"seats": 1})
	id, path := o.str("id"), "/api/v1/orders/"+o.str("id")
	price1 := o.Body["price"].(float64)

	r := p.do("PATCH", path, map[string]any{"seats": 3, "comment": "yuk bor"})
	if r.Status != 200 || r.Body["seats"] != float64(3) || r.str("comment") != "yuk bor" || r.Body["price"].(float64) <= price1 || r.str("status") != "searching" {
		t.Fatalf("seats: %d %s", r.Status, r.Raw)
	}
	r = p.do("PATCH", path, map[string]any{"car_class": "business"})
	if r.Status != 200 || r.str("car_class") != "business" || r.Body["seats"] != nil {
		t.Fatalf("car_class seats'ni almashtiradi: %d %s", r.Status, r.Raw)
	}
	farTo := map[string]any{"lat": 41.20, "lng": 69.10, "address": "Chekka"}
	r2 := p.do("PATCH", path, map[string]any{"to": farTo, "payment_method": "click"})
	if r2.Status != 200 || r2.str("payment_method") != "click" || r2.obj("to")["address"] != "Chekka" || r2.Body["distance_m"].(float64) <= r.Body["distance_m"].(float64) {
		t.Fatalf("to: %d %s", r2.Status, r2.Raw)
	}
	if g := p.do("GET", path, nil); g.Body["price"] != r2.Body["price"] {
		t.Fatal("yangilangan narx saqlanmadi")
	}

	for name, body := range map[string]any{
		"bo'sh":               map[string]any{},
		"seats va car_class":  map[string]any{"seats": 2, "car_class": "economy"},
		"narx yuborish":       map[string]any{"price": 5},
		"noma'lum usul":       map[string]any{"payment_method": "x"},
		"to == from":          map[string]any{"to": map[string]any{"lat": 41.3111, "lng": 69.2797}},
		"scheduled o'tmishda": map[string]any{"scheduled_at": e.Clock.Now().Add(-time.Hour).Format(time.RFC3339)},
		"passenger + vehicle": map[string]any{"vehicle_type": "labo"},
	} {
		if r := p.do("PATCH", path, body); r.Status != 422 {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	if r := other.do("PATCH", path, map[string]any{"seats": 1}); r.Status != 404 {
		t.Errorf("begona buyurtma: %d", r.Status)
	}

	// delivery: seats mumkin emas
	dl := p.order(t, map[string]any{"type": "delivery", "seats": nil, "vehicle_type": "labo"})
	if r := p.do("PATCH", "/api/v1/orders/"+dl.str("id"), map[string]any{"seats": 2}); r.Status != 422 {
		t.Errorf("delivery seats: %d %s", r.Status, r.Raw)
	}
	if r := p.do("PATCH", "/api/v1/orders/"+dl.str("id"), map[string]any{"vehicle_type": "fura"}); r.Status != 200 || r.str("vehicle_type") != "fura" {
		t.Errorf("delivery vehicle_type: %d %s", r.Status, r.Raw)
	}

	// faqat searching'da: accepted -> 403 driver_assigned, in_progress -> trip_started
	offer := d.offerFor(t, id)
	tripID := d.do("POST", "/api/v1/trips/offers/"+offer+"/accept", nil).str("id")
	if r := p.do("PATCH", path, map[string]any{"seats": 1}); r.Status != 403 || r.errKey() != "FORBIDDEN" || r.errParam("reason") != "driver_assigned" {
		t.Errorf("accepted: %d %s", r.Status, r.Raw)
	}
	d.do("POST", "/api/v1/trips/"+tripID+"/start", nil)
	if r := p.do("PATCH", path, map[string]any{"seats": 1}); r.Status != 403 || r.errParam("reason") != "trip_started" {
		t.Errorf("in_progress: %d %s", r.Status, r.Raw)
	}
}

func TestCancelOrder(t *testing.T) {
	e := newT(t)
	p, other, d := e.passenger(), e.passenger(), e.driver()
	e.AddDrivers(d.ID)
	o := p.order(t, nil)
	id := o.str("id")
	offer := d.offerFor(t, id)

	if r := other.do("POST", "/api/v1/orders/"+id+"/cancel", nil); r.Status != 404 {
		t.Fatalf("begona: %d", r.Status)
	}
	r := p.do("POST", "/api/v1/orders/"+id+"/cancel", map[string]any{"reason": "changed_plans", "comment": "kerak emas"})
	if r.Status != 200 || r.str("status") != "cancelled" {
		t.Fatalf("cancel: %d %s", r.Status, r.Raw)
	}
	// takroriy chaqiruv xato emas va hisoblagichni ikki marta kamaytirmaydi
	if r := p.do("POST", "/api/v1/orders/"+id+"/cancel", nil); r.Status != 200 || r.str("status") != "cancelled" {
		t.Fatalf("takroriy: %d %s", r.Status, r.Raw)
	}
	if n, _ := e.Guard.Count(t.Context(), "p", p.ID); n != 0 {
		t.Fatalf("hisoblagich = %d, 0 bo'lishi kerak", n)
	}
	// taklif yopildi: ro'yxatda yo'q, qabul qilib bo'lmaydi
	if l := d.do("GET", "/api/v1/trips/offers", nil); len(l.Body["data"].([]any)) != 0 {
		t.Errorf("bekor qilingan buyurtma taklifi ko'rinmoqda: %s", l.Raw)
	}
	if a := d.do("POST", "/api/v1/trips/offers/"+offer+"/accept", nil); a.Status != 404 || a.errKey() != "ORDER_NOT_FOUND" {
		t.Errorf("bekor qilingan buyurtmani qabul qilish: %d %s", a.Status, a.Raw)
	}
	for _, bad := range []map[string]any{{"reason": "bored"}, {"extra": 1}} {
		if r := p.do("POST", "/api/v1/orders/"+id+"/cancel", bad); r.Status != 422 {
			t.Errorf("%v: %d", bad, r.Status)
		}
	}

	// qabul qilingan buyurtmani bekor qilib bo'lmaydi
	o2 := p.order(t, nil)
	d.do("POST", "/api/v1/trips/offers/"+d.offerFor(t, o2.str("id"))+"/accept", nil)
	if r := p.do("POST", "/api/v1/orders/"+o2.str("id")+"/cancel", nil); r.Status != 403 || r.errParam("reason") != "driver_assigned" {
		t.Errorf("accepted cancel: %d %s", r.Status, r.Raw)
	}
}

func TestHistoryPagination(t *testing.T) {
	e := newT(t, testenvPassengerLimit(100))
	p, other := e.passenger(), e.passenger()
	var want []string
	for i := 0; i < 7; i++ {
		if i != 3 && i != 4 { // 3 va 4 bir xil vaqtda (created_at teng) — id bo'yicha tartib
			e.advance(time.Second)
		}
		want = append([]string{p.order(t, nil).str("id")}, want...) // yangidan eskiga
	}
	other.order(t, nil)

	var got []string
	cursor := ""
	for page := 0; page < 10; page++ {
		path := "/api/v1/orders/history?limit=3"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := p.do("GET", path, nil)
		if r.Status != 200 {
			t.Fatalf("history: %d %s", r.Status, r.Raw)
		}
		for _, it := range r.Body["data"].([]any) {
			got = append(got, it.(map[string]any)["id"].(string))
		}
		if r.Body["cursor"] == nil {
			break
		}
		cursor = r.Body["cursor"].(string)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tarix tartibi/to'liqligi:\n got  %v\n want %v", got, want)
	}
	if r := p.do("GET", "/api/v1/orders/history", nil); len(r.Body["data"].([]any)) != 7 || r.Body["cursor"] != nil {
		t.Errorf("default limit: %s", r.Raw)
	}
	for _, q := range []string{"?limit=0", "?limit=101", "?limit=abc", "?cursor=@@@"} {
		if r := p.do("GET", "/api/v1/orders/history"+q, nil); r.Status != 422 {
			t.Errorf("%s: %d", q, r.Status)
		}
	}
}

func TestActiveOrdersLimit(t *testing.T) {
	e := newT(t)
	p := e.passenger()
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, p.order(t, nil).str("id"))
	}
	r := p.do("POST", "/api/v1/orders", trip(nil))
	if r.Status != 403 || r.errKey() != "FORBIDDEN" || r.errParam("reason") != "active_orders_limit" || r.errParam("limit") != float64(3) {
		t.Fatalf("4-buyurtma: %d %s", r.Status, r.Raw)
	}
	if n := len(e.Outbox()); n != 3 {
		t.Fatalf("rad etilgan buyurtma hodisa chiqardi: %d", n)
	}
	// bitta bekor qilinsa joy bo'shaydi
	p.do("POST", "/api/v1/orders/"+ids[0]+"/cancel", nil)
	if r := p.do("POST", "/api/v1/orders", trip(nil)); r.Status != 201 {
		t.Fatalf("joy bo'shagach: %d %s", r.Status, r.Raw)
	}
	// boshqa yo'lovchiga ta'sir qilmaydi
	if r := e.passenger().do("POST", "/api/v1/orders", trip(nil)); r.Status != 201 {
		t.Fatalf("boshqa yo'lovchi: %d", r.Status)
	}
	// Redis hisoblagichi yo'qolsa ham bazadagi son bo'yicha tiklanadi (cheklov saqlanadi)
	e.MR.FlushAll()
	if r := p.do("POST", "/api/v1/orders", trip(nil)); r.Status != 403 {
		t.Fatalf("Redis tozalangach limit buzildi: %d %s", r.Status, r.Raw)
	}
	// hisoblagich ortiqcha ko'rsatsa (sızish) — baza bo'yicha tuzaladi
	p2 := e.passenger()
	e.MR.Set("active:p:"+p2.ID.String(), "3")
	if r := p2.do("POST", "/api/v1/orders", trip(nil)); r.Status != 201 {
		t.Fatalf("sizib qolgan hisoblagich tuzalmadi: %d %s", r.Status, r.Raw)
	}
}

func TestIdempotencyKey(t *testing.T) {
	e := newT(t)
	p := e.passenger()
	key := uuid.NewString()
	a := p.with("Idempotency-Key", key)
	r1 := a.do("POST", "/api/v1/orders", trip(nil))
	r2 := a.do("POST", "/api/v1/orders", trip(nil))
	if r1.Status != 201 || r2.Status != 201 || r1.str("id") != r2.str("id") {
		t.Fatalf("takroriy so'rov: %d/%d %s %s", r1.Status, r2.Status, r1.Raw, r2.Raw)
	}
	if n := len(e.Outbox()); n != 1 {
		t.Errorf("hodisalar = %d, 1 bo'lishi kerak", n)
	}
	if n, _ := e.Guard.Count(t.Context(), "p", p.ID); n != 1 {
		t.Errorf("hisoblagich = %d", n)
	}
	// parallel takror: bitta buyurtma
	k2 := p.with("Idempotency-Key", uuid.NewString())
	ids := make(chan string, 6)
	for i := 0; i < 6; i++ {
		go func() { ids <- k2.do("POST", "/api/v1/orders", trip(nil)).str("id") }()
	}
	first := <-ids
	for i := 1; i < 6; i++ {
		if id := <-ids; id != first {
			t.Errorf("parallel takror turli buyurtma yaratdi: %s != %s", id, first)
		}
	}
	if r := p.with("Idempotency-Key", "xyz").do("POST", "/api/v1/orders", trip(nil)); r.Status != 422 {
		t.Errorf("noto'g'ri kalit: %d", r.Status)
	}
	// boshqa yo'lovchi bir xil kalit bilan o'z buyurtmasini oladi
	if r := e.passenger().with("Idempotency-Key", key).do("POST", "/api/v1/orders", trip(nil)); r.Status != 201 || r.str("id") == r1.str("id") {
		t.Errorf("kalit yo'lovchilar bo'yicha alohida bo'lishi kerak: %d", r.Status)
	}
}

func TestExpiry(t *testing.T) {
	e := newT(t)
	p, d := e.passenger(), e.driver()
	e.AddDrivers(d.ID)
	o := p.order(t, nil)
	id := o.str("id")
	offer := d.offerFor(t, id)

	if n, err := e.Svc.Sweep(t.Context()); err != nil || n != 0 {
		t.Fatalf("muddatidan oldin: %d %v", n, err)
	}
	e.advance(2*time.Minute + time.Second)
	// muddati o'tgan taklif sweeper ishlamasa ham qabul qilinmaydi
	if a := d.do("POST", "/api/v1/trips/offers/"+offer+"/accept", nil); a.Status != 404 {
		t.Errorf("muddati o'tgan taklif: %d %s", a.Status, a.Raw)
	}
	if l := d.do("GET", "/api/v1/trips/offers", nil); len(l.Body["data"].([]any)) != 0 {
		t.Errorf("muddati o'tgan taklif ro'yxatda: %s", l.Raw)
	}
	if n, err := e.Svc.Sweep(t.Context()); err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	if g := p.do("GET", "/api/v1/orders/"+id, nil); g.str("status") != "expired" {
		t.Fatalf("status: %s", g.Raw)
	}
	if r := p.do("POST", "/api/v1/orders/"+id+"/cancel", nil); r.Status != 403 || r.errParam("reason") != "order_closed" {
		t.Errorf("expired bekor qilinmaydi: %d %s", r.Status, r.Raw)
	}
	if n, _ := e.Guard.Count(t.Context(), "p", p.ID); n != 0 {
		t.Errorf("hisoblagich bo'shamadi: %d", n)
	}
	if n, _ := e.Svc.Sweep(t.Context()); n != 0 {
		t.Errorf("takroriy sweep: %d", n)
	}
	// haydovchi topilmagan (taklifsiz) buyurtma ham expired bo'ladi
	o2 := p.order(t, map[string]any{"seats": 1})
	e.advance(3 * time.Minute)
	e.Svc.Sweep(t.Context())
	if g := p.do("GET", "/api/v1/orders/"+o2.str("id"), nil); g.str("status") != "expired" {
		t.Errorf("taklifsiz buyurtma: %s", g.Raw)
	}
}
