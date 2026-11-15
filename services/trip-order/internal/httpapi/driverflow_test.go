package httpapi_test

import (
	"strings"
	"testing"

	"github.com/izgo/trip-order/internal/testenv"
)

func subjects(e *tenv) map[string]int {
	out := map[string]int{}
	for _, ev := range e.Outbox() {
		out[ev.Subject]++
	}
	return out
}

func actions(r resp) []any { a, _ := r.Body["next_actions"].([]any); return a }

// accepted -> arrived -> in_progress -> completed; hodisalar va next_actions.
func TestArriveStartComplete(t *testing.T) {
	e := newT(t)
	d := e.driver()
	e.AddDrivers(d.ID)
	p := e.passenger()
	o := p.order(t, nil)
	acc := d.do("POST", acceptPath(d.offerFor(t, o.str("id"))), nil)
	if acc.Status != 200 || len(actions(acc)) != 3 || actions(acc)[0] != "arrive" {
		t.Fatalf("accept: %d %s", acc.Status, acc.Raw)
	}
	tid := acc.str("id")

	ar := d.do("POST", "/api/v1/trips/"+tid+"/arrive", nil)
	if ar.Status != 200 || ar.str("arrived_at") == "" || len(actions(ar)) != 2 || actions(ar)[0] != "start" {
		t.Fatalf("arrive: %d %s", ar.Status, ar.Raw)
	}
	if g := p.do("GET", "/api/v1/orders/"+o.str("id"), nil); g.str("status") != "arrived" || g.Body["can_cancel"] != false {
		t.Fatalf("order arrived emas: %s", g.Raw)
	}
	if again := d.do("POST", "/api/v1/trips/"+tid+"/arrive", nil); again.Status != 200 {
		t.Fatalf("takroriy arrive: %d %s", again.Status, again.Raw)
	}
	if r := e.driver().do("POST", "/api/v1/trips/"+tid+"/arrive", nil); r.Status != 404 {
		t.Fatalf("begona haydovchi arrive: %d", r.Status)
	}
	st := d.do("POST", "/api/v1/trips/"+tid+"/start", nil)
	if st.Status != 200 || st.str("status") != "in_progress" || len(actions(st)) != 1 || actions(st)[0] != "complete" {
		t.Fatalf("start: %d %s", st.Status, st.Raw)
	}
	if r := d.do("POST", "/api/v1/trips/"+tid+"/arrive", nil); r.Status != 403 {
		t.Fatalf("boshlangandan keyin arrive: %d", r.Status)
	}
	if r := d.do("POST", "/api/v1/trips/"+tid+"/complete", nil); r.Status != 200 || len(actions(r)) != 0 {
		t.Fatalf("complete: %d %s", r.Status, r.Raw)
	}
	sub := subjects(e)
	if sub["izgo.order.arrived.v1"] != 1 || sub["izgo.trip.started.v1"] != 1 || sub["izgo.trip.completed.v1"] != 1 {
		t.Fatalf("hodisalar: %v", sub)
	}
	// tarix: yakunlangan safar buyurtmasi bilan
	h := d.do("GET", "/api/v1/trips/history", nil)
	data, _ := h.Body["data"].([]any)
	if h.Status != 200 || len(data) != 1 || data[0].(map[string]any)["order"].(map[string]any)["id"] != o.str("id") {
		t.Fatalf("history: %d %s", h.Status, h.Raw)
	}
	if a := d.do("GET", "/api/v1/trips", nil); len(a.Body["data"].([]any)) != 0 {
		t.Fatalf("faol ro'yxat bo'sh bo'lishi kerak: %s", a.Raw)
	}
	if r := d.do("GET", "/api/v1/trips?status=bogus", nil); r.Status != 422 {
		t.Fatalf("status filtri: %d", r.Status)
	}
	if r := p.do("GET", "/api/v1/trips/history", nil); r.Status != 403 {
		t.Fatalf("yo'lovchi trips: %d", r.Status)
	}
}

func TestRejectOffer(t *testing.T) {
	e := newT(t)
	d := e.driver()
	e.AddDrivers(d.ID)
	o := e.passenger().order(t, nil)
	of := d.offerFor(t, o.str("id"))
	if r := d.do("POST", "/api/v1/trips/offers/"+of+"/reject", map[string]any{"reason": "too_far"}); r.Status != 204 {
		t.Fatalf("reject: %d %s", r.Status, r.Raw)
	}
	if r := d.do("POST", "/api/v1/trips/offers/"+of+"/reject", nil); r.Status != 204 {
		t.Fatalf("takroriy reject: %d %s", r.Status, r.Raw)
	}
	if r := d.do("GET", "/api/v1/trips/offers", nil); len(r.Body["data"].([]any)) != 0 {
		t.Fatalf("rad etilgan taklif feed'da qoldi: %s", r.Raw)
	}
	if r := d.do("POST", acceptPath(of), nil); r.Status != 404 {
		t.Fatalf("rad etilganni qabul qilish: %d", r.Status)
	}
	if r := d.do("POST", "/api/v1/trips/offers/"+of+"/reject", map[string]any{"reason": "x"}); r.Status != 422 {
		t.Fatalf("noto'g'ri sabab: %d", r.Status)
	}
	if r := e.driver().do("POST", "/api/v1/trips/offers/"+of+"/reject", nil); r.Status != 404 {
		t.Fatalf("begona reject: %d", r.Status)
	}
}

// Haydovchi bekor qiladi: buyurtma qayta qidiruvga, boshqa haydovchi oladi; bekor qilgan haydovchiga qayta taklif yo'q.
func TestDriverCancelRedispatches(t *testing.T) {
	e := newT(t)
	d1, d2 := e.driver(), e.driver()
	e.AddDrivers(d1.ID, d2.ID)
	p := e.passenger()
	o := p.order(t, nil)
	acc := d1.do("POST", acceptPath(d1.offerFor(t, o.str("id"))), nil)
	tid := acc.str("id")

	if r := d1.do("POST", "/api/v1/trips/"+tid+"/cancel", map[string]any{"reason": "nope"}); r.Status != 422 {
		t.Fatalf("noto'g'ri sabab: %d", r.Status)
	}
	c := d1.do("POST", "/api/v1/trips/"+tid+"/cancel", map[string]any{"reason": "car_problem", "comment": "g'ildirak"})
	if c.Status != 200 || c.str("status") != "cancelled" || c.str("cancelled_by") != "driver" {
		t.Fatalf("cancel: %d %s", c.Status, c.Raw)
	}
	g := p.do("GET", "/api/v1/orders/"+o.str("id"), nil)
	if g.str("status") != "searching" || g.Body["driver"] != nil || g.Body["trip_id"] != nil {
		t.Fatalf("buyurtma qayta qidiruvda emas: %s", g.Raw)
	}
	if r := d1.do("GET", "/api/v1/trips/offers", nil); len(r.Body["data"].([]any)) != 0 {
		t.Fatalf("bekor qilgan haydovchiga qayta taklif: %s", r.Raw)
	}
	acc2 := d2.do("POST", acceptPath(d2.offerFor(t, o.str("id"))), nil)
	if acc2.Status != 200 {
		t.Fatalf("ikkinchi haydovchi ololmadi: %d %s", acc2.Status, acc2.Raw)
	}
	// d1 bo'shadi: yangi buyurtma ola oladi (faol safar limiti)
	o2 := e.passenger().order(t, nil)
	if r := d1.do("POST", acceptPath(d1.offerFor(t, o2.str("id"))), nil); r.Status != 200 {
		t.Fatalf("d1 limit bo'shamadi: %d %s", r.Status, r.Raw)
	}
	// boshlangan safarni haydovchi bekor qila olmaydi
	d2.do("POST", "/api/v1/trips/"+acc2.str("id")+"/start", nil)
	if r := d2.do("POST", "/api/v1/trips/"+acc2.str("id")+"/cancel", map[string]any{"reason": "other"}); r.Status != 403 || r.errParam("reason") != "trip_started" {
		t.Fatalf("boshlangan safar: %d %s", r.Status, r.Raw)
	}
	sub := subjects(e)
	// offer.created: o (d1, d2) + qayta qidiruvda d2 (qayta ochilgan) + o2 (d1, d2 — d2 band bo'lsa ham mock matching beradi)
	if sub["izgo.trip.cancelled.v1"] != 1 || sub["izgo.offer.created.v1"] != 5 {
		t.Fatalf("hodisalar: %v", sub)
	}
	// offer.created dagi offer_id — haydovchi qabul qilishda ishlatadigan ID
	for _, ev := range e.Outbox() {
		if ev.Subject == "izgo.offer.created.v1" && !strings.Contains(string(ev.Payload), `"offer_id"`) {
			t.Fatalf("offer.created payload: %s", ev.Payload)
		}
	}
}

func TestInternalAdminOrders(t *testing.T) {
	e := newT(t)
	d := e.driver()
	e.AddDrivers(d.ID)
	p := e.passenger()
	o := p.order(t, nil)
	in := e.as("").with("X-Internal-Token", testenv.InternalToken)

	if r := e.as("").do("GET", "/internal/orders", nil); r.Status != 403 {
		t.Fatalf("token'siz ichki API: %d", r.Status)
	}
	if r := e.as("").with("X-Internal-Token", "wrong-token-0123456789").do("GET", "/internal/orders", nil); r.Status != 403 {
		t.Fatalf("noto'g'ri token: %d", r.Status)
	}
	l := in.do("GET", "/internal/orders?passenger_id="+p.ID.String(), nil)
	data, _ := l.Body["data"].([]any)
	if l.Status != 200 || len(data) != 1 || data[0].(map[string]any)["passenger_id"] != p.ID.String() {
		t.Fatalf("list: %d %s", l.Status, l.Raw)
	}
	if r := in.do("GET", "/internal/orders?status=bogus", nil); r.Status != 422 {
		t.Fatalf("status filtri: %d", r.Status)
	}
	u := in.do("PATCH", "/internal/orders/"+o.str("id"), map[string]any{"comment": "operator: 2-podyezd", "payment_method": "card"})
	if u.Status != 200 || u.str("comment") != "operator: 2-podyezd" || u.str("payment_method") != "card" || u.Body["price"] != o.Body["price"] {
		t.Fatalf("patch: %d %s", u.Status, u.Raw)
	}

	// biriktirilgan buyurtmani admin bekor qiladi: safar ham bekor, haydovchi bo'shaydi
	d.do("POST", acceptPath(d.offerFor(t, o.str("id"))), nil)
	if r := in.do("PATCH", "/internal/orders/"+o.str("id"), map[string]any{"comment": "x"}); r.Status != 403 || r.errParam("reason") != "driver_assigned" {
		t.Fatalf("biriktirilganni tahrirlash: %d %s", r.Status, r.Raw)
	}
	c := in.do("POST", "/internal/orders/"+o.str("id")+"/cancel", map[string]any{"reason": "fraud", "comment": "test"})
	if c.Status != 200 || c.str("status") != "cancelled" || c.str("cancel_reason") != "fraud" {
		t.Fatalf("admin cancel: %d %s", c.Status, c.Raw)
	}
	if again := in.do("POST", "/internal/orders/"+o.str("id")+"/cancel", map[string]any{"reason": "fraud"}); again.Status != 200 {
		t.Fatalf("takroriy cancel: %d", again.Status)
	}
	if r := in.do("POST", "/internal/orders/"+o.str("id")+"/cancel", map[string]any{"reason": "bad"}); r.Status != 422 {
		t.Fatalf("noto'g'ri sabab: %d", r.Status)
	}
	sub := subjects(e)
	if sub["izgo.order.cancelled.v1"] != 1 || sub["izgo.trip.cancelled.v1"] != 1 {
		t.Fatalf("hodisalar: %v", sub)
	}
	o2 := p.order(t, nil)
	acc := d.do("POST", acceptPath(d.offerFor(t, o2.str("id"))), nil)
	if acc.Status != 200 {
		t.Fatalf("haydovchi bo'shamadi: %d %s", acc.Status, acc.Raw)
	}
	d.do("POST", "/api/v1/trips/"+acc.str("id")+"/start", nil)
	if r := in.do("POST", "/internal/orders/"+o2.str("id")+"/cancel", map[string]any{"reason": "other"}); r.Status != 403 || r.errParam("reason") != "trip_started" {
		t.Fatalf("boshlangan safar: %d %s", r.Status, r.Raw)
	}
	d.do("POST", "/api/v1/trips/"+acc.str("id")+"/complete", nil)

	s := in.do("GET", "/internal/orders/stats", nil)
	by, _ := s.Body["orders_by_status"].(map[string]any)
	if s.Status != 200 || s.Body["orders_total"] != float64(2) || s.Body["orders_completed"] != float64(1) || by["cancelled"] != float64(1) ||
		s.Body["revenue"] != o2.Body["price"] || s.Body["average_order_value"] != o2.Body["price"] {
		t.Fatalf("stats: %d %s", s.Status, s.Raw)
	}
	routes, _ := s.Body["top_routes"].([]any)
	daily, _ := s.Body["daily"].([]any)
	if len(routes) != 1 || routes[0].(map[string]any)["from_name"] != "Toshkent" || len(daily) != 1 {
		t.Fatalf("stats routes/daily: %s", s.Raw)
	}
	if r := in.do("GET", "/internal/orders/stats?from_date=2026-13-01", nil); r.Status != 422 {
		t.Fatalf("sana: %d", r.Status)
	}
	if r := in.do("GET", "/internal/orders/"+o.str("id"), nil); r.Status != 200 || r.str("passenger_id") != p.ID.String() {
		t.Fatalf("get: %d %s", r.Status, r.Raw)
	}
}
