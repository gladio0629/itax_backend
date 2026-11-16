package httpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/events"
)

type eventT = events.Event

func acceptPath(offer string) string { return "/api/v1/trips/offers/" + offer + "/accept" }

func TestOffersListIsolation(t *testing.T) {
	e := newT(t)
	d1, d2, outsider := e.driver(), e.driver(), e.driver()
	e.AddDrivers(d1.ID, d2.ID)
	p := e.passenger()
	o := p.order(t, map[string]any{"seats": 3})

	r := d1.do("GET", "/api/v1/trips/offers", nil)
	data := r.Body["data"].([]any)
	if r.Status != 200 || len(data) != 1 || r.Body["cursor"] != nil {
		t.Fatalf("offers: %d %s", r.Status, r.Raw)
	}
	of := data[0].(map[string]any)
	if of["order_id"] != o.str("id") || of["price"] != o.Body["price"] || of["seats"] != float64(3) || of["type"] != "passenger" ||
		of["commission"].(float64) <= 0 || of["pickup_eta_s"].(float64) <= 0 || of["expires_at"] == nil || of["distance_m"] == nil {
		t.Fatalf("taklif: %s", r.Raw)
	}
	// F-10: takliflar faqat o'z haydovchisiga
	if r := outsider.do("GET", "/api/v1/trips/offers", nil); len(r.Body["data"].([]any)) != 0 {
		t.Errorf("begona haydovchi taklif ko'rdi: %s", r.Raw)
	}
	if r := outsider.do("POST", acceptPath(of["id"].(string)), nil); r.Status != 403 || r.errParam("reason") != "not_owner" {
		t.Errorf("begona haydovchi qabul qila oldi: %d", r.Status)
	}
	for _, q := range []string{"?limit=0", "?cursor=!!"} {
		if r := d1.do("GET", "/api/v1/trips/offers"+q, nil); r.Status != 422 {
			t.Errorf("%s: %d", q, r.Status)
		}
	}
}

func TestOffersPagination(t *testing.T) {
	e := newT(t, testenvPassengerLimit(100))
	d := e.driver()
	e.AddDrivers(d.ID)
	p := e.passenger()
	want := map[string]bool{}
	for i := 0; i < 5; i++ {
		e.advance(time.Second)
		want[p.order(t, nil).str("id")] = true
	}
	got := map[string]bool{}
	cursor := ""
	for i := 0; i < 10; i++ {
		path := "/api/v1/trips/offers?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := d.do("GET", path, nil)
		for _, it := range r.Body["data"].([]any) {
			id := it.(map[string]any)["order_id"].(string)
			if got[id] {
				t.Fatalf("takror: %s", id)
			}
			got[id] = true
		}
		if r.Body["cursor"] == nil {
			break
		}
		cursor = r.Body["cursor"].(string)
	}
	if len(got) != 5 {
		t.Fatalf("got %d, want 5", len(got))
	}
}

func TestAcceptFirstWins(t *testing.T) {
	e := newT(t)
	d1, d2 := e.driver(), e.driver()
	e.AddDrivers(d1.ID, d2.ID)
	p := e.passenger()
	o := p.order(t, nil)
	id := o.str("id")
	off1, off2 := d1.offerFor(t, id), d2.offerFor(t, id)

	r := d1.do("POST", acceptPath(off1), nil)
	if r.Status != 200 || r.str("status") != "assigned" || r.str("order_id") != id || r.str("driver_id") != d1.ID.String() ||
		r.Body["started_at"] != nil || r.Body["final_price"] != nil {
		t.Fatalf("accept: %d %s", r.Status, r.Raw)
	}
	tripID := r.str("id")

	// ikkinchi haydovchi — ORDER_ALREADY_TAKEN (409)
	l := d2.do("POST", acceptPath(off2), nil)
	if l.Status != 409 || l.errKey() != "ORDER_ALREADY_TAKEN" || l.errParam("order_id") != id {
		t.Fatalf("ikkinchi haydovchi: %d %s", l.Status, l.Raw)
	}
	// yutqazgan haydovchi joy band qilib qolmaydi
	if n, _ := e.Guard.Count(t.Context(), "d", d2.ID); n != 0 {
		t.Errorf("yutqazgan haydovchi hisoblagichi = %d", n)
	}
	if l := d2.do("GET", "/api/v1/trips/offers", nil); len(l.Body["data"].([]any)) != 0 {
		t.Errorf("olingan buyurtma taklifi qolgan: %s", l.Raw)
	}

	// buyurtma: accepted + driver + trip_id
	g := p.do("GET", "/api/v1/orders/"+id, nil)
	if g.str("status") != "accepted" || g.obj("driver")["id"] != d1.ID.String() || g.str("trip_id") != tripID {
		t.Fatalf("buyurtma: %s", g.Raw)
	}
	// qayta urinish (tarmoq uzilishi): xuddi shu haydovchiga xuddi shu safar, ikkinchi safar yaratilmaydi
	if r2 := d1.do("POST", acceptPath(off1), nil); r2.Status != 200 || r2.str("id") != tripID {
		t.Errorf("idempotent accept: %d %s", r2.Status, r2.Raw)
	}
	var trips int
	_ = e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM trips`).Scan(&trips)
	if trips != 1 {
		t.Errorf("safarlar soni %d", trips)
	}

	// order.accepted hodisasi
	var found bool
	for _, ev := range e.Outbox() {
		if ev.Subject != "izgo.order.accepted.v1" {
			continue
		}
		found = true
		var m struct {
			Event   string
			Payload map[string]any
		}
		_ = json.Unmarshal(ev.Payload, &m)
		pl := m.Payload
		if m.Event != "order.accepted" || pl["order_id"] != id || pl["trip_id"] != tripID || pl["driver_id"] != d1.ID.String() || pl["offer_id"] != off1 || pl["accepted_at"] == nil {
			t.Errorf("order.accepted: %s", ev.Payload)
		}
	}
	if !found {
		t.Error("order.accepted hodisasi yo'q")
	}
	if n := countSubject(e, "izgo.order.accepted.v1"); n != 1 {
		t.Errorf("order.accepted %d marta", n)
	}
}

func countSubject(e *tenv, subj string) int {
	n := 0
	for _, ev := range e.Outbox() {
		if ev.Subject == subj {
			n++
		}
	}
	return n
}

// 20 haydovchi bir vaqtda: aynan bittasi oladi, 19 tasi 409.
func TestAcceptRace(t *testing.T) {
	e := newT(t)
	p := e.passenger()
	drivers := make([]user, 20)
	for i := range drivers {
		drivers[i] = e.driver()
		e.AddDrivers(drivers[i].ID)
	}
	o := p.order(t, nil)
	id := o.str("id")
	offers := make([]string, len(drivers))
	for i, d := range drivers {
		offers[i] = d.offerFor(t, id)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]resp, len(drivers))
	for i := range drivers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = drivers[i].do("POST", acceptPath(offers[i]), nil)
		}(i)
	}
	close(start)
	wg.Wait()

	winners, losers := 0, 0
	winner := -1
	for i, r := range results {
		switch {
		case r.Status == 200:
			winners++
			winner = i
		case r.Status == 409 && r.errKey() == "ORDER_ALREADY_TAKEN":
			losers++
		default:
			t.Errorf("driver %d: kutilmagan javob %d %s", i, r.Status, r.Raw)
		}
	}
	if winners != 1 || losers != 19 {
		t.Fatalf("winners=%d losers=%d", winners, losers)
	}
	var trips, accepted int
	_ = e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM trips WHERE order_id=$1`, id).Scan(&trips)
	_ = e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM offers WHERE order_id=$1 AND status='accepted'`, id).Scan(&accepted)
	if trips != 1 || accepted != 1 {
		t.Errorf("trips=%d accepted offers=%d", trips, accepted)
	}
	if n := countSubject(e, "izgo.order.accepted.v1"); n != 1 {
		t.Errorf("order.accepted %d marta", n)
	}
	for i, d := range drivers {
		want := 0
		if i == winner {
			want = 1
		}
		if n, _ := e.Guard.Count(t.Context(), "d", d.ID); n != want {
			t.Errorf("driver %d hisoblagichi %d, kutilgan %d", i, n, want)
		}
	}
}

// SETNX qulfi: boshqa haydovchi qulfni ushlab turganda ikkinchisi bazaga ham bormaydi.
func TestAcceptLockSETNX(t *testing.T) {
	e := newT(t)
	d1, d2 := e.driver(), e.driver()
	e.AddDrivers(d1.ID, d2.ID)
	p := e.passenger()
	id := p.order(t, nil).str("id")
	off2 := d2.offerFor(t, id)

	// d1 qulfni olgan, hali bazaga yozmagan (jarayon o'rtasida)
	e.MR.Set("order:accept-lock:"+id, d1.ID.String())
	r := d2.do("POST", acceptPath(off2), nil)
	if r.Status != 409 || r.errKey() != "ORDER_ALREADY_TAKEN" {
		t.Fatalf("qulf band: %d %s", r.Status, r.Raw)
	}
	var st string
	_ = e.Pool.QueryRow(t.Context(), `SELECT status FROM orders WHERE id=$1`, id).Scan(&st)
	if st != "searching" {
		t.Errorf("qulf tufayli baza o'zgardi: %s", st)
	}
	if n, _ := e.Guard.Count(t.Context(), "d", d2.ID); n != 0 {
		t.Errorf("d2 joy band qilib qoldi: %d", n)
	}
	// qulf muddati o'tgach d2 oladi
	e.MR.FastForward(0)
	e.MR.Del("order:accept-lock:" + id)
	if r := d2.do("POST", acceptPath(off2), nil); r.Status != 200 {
		t.Fatalf("qulf bo'shagach: %d %s", r.Status, r.Raw)
	}
}

// Qulf yo'qolsa (TTL tugadi / Redis tozalandi) ham bazadagi atomar UPDATE ikkinchi marta olishga yo'l qo'ymaydi.
func TestAcceptDBGuardWithoutLock(t *testing.T) {
	e := newT(t)
	d1, d2 := e.driver(), e.driver()
	e.AddDrivers(d1.ID, d2.ID)
	p := e.passenger()
	id := p.order(t, nil).str("id")
	off1, off2 := d1.offerFor(t, id), d2.offerFor(t, id)

	if r := d1.do("POST", acceptPath(off1), nil); r.Status != 200 {
		t.Fatal(r.Status, string(r.Raw))
	}
	e.MR.Del("order:accept-lock:" + id)
	r := d2.do("POST", acceptPath(off2), nil)
	if r.Status != 409 || r.errKey() != "ORDER_ALREADY_TAKEN" {
		t.Fatalf("qulfsiz: %d %s", r.Status, r.Raw)
	}
	if n, _ := e.Guard.Count(t.Context(), "d", d2.ID); n != 0 {
		t.Errorf("d2 hisoblagichi %d", n)
	}
}

func TestWalletLimitBlocksAccept(t *testing.T) {
	e := newT(t)
	debtor, other := e.driver(), e.driver()
	e.AddDrivers(debtor.ID, other.ID)
	p := e.passenger()
	id := p.order(t, nil).str("id")
	e.Wallet.Block(debtor.ID, -5000000)

	r := debtor.do("POST", acceptPath(debtor.offerFor(t, id)), nil)
	if r.Status != 402 || r.errKey() != "WALLET_LIMIT_REACHED" || r.errParam("limit") != float64(-5000000) {
		t.Fatalf("qarz chegarasi: %d %s", r.Status, r.Raw)
	}
	// qulf ushlanmadi, buyurtma boshqa haydovchiga ochiq
	if keys := e.MR.Keys(); containsPrefix(keys, "order:accept-lock:") {
		t.Errorf("qarzdor haydovchi qulf qoldirdi: %v", keys)
	}
	if n, _ := e.Guard.Count(t.Context(), "d", debtor.ID); n != 0 {
		t.Errorf("hisoblagich %d", n)
	}
	if r := other.do("POST", acceptPath(other.offerFor(t, id)), nil); r.Status != 200 {
		t.Errorf("boshqa haydovchi: %d %s", r.Status, r.Raw)
	}
}

func containsPrefix(keys []string, prefix string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func TestDriverActiveTripLimit(t *testing.T) {
	e := newT(t)
	d := e.driver()
	e.AddDrivers(d.ID)
	p1, p2 := e.passenger(), e.passenger()
	a, b := p1.order(t, nil).str("id"), p2.order(t, nil).str("id")
	offA, offB := d.offerFor(t, a), d.offerFor(t, b)

	tripA := d.do("POST", acceptPath(offA), nil)
	if tripA.Status != 200 {
		t.Fatal(tripA.Status, string(tripA.Raw))
	}
	r := d.do("POST", acceptPath(offB), nil)
	if r.Status != 403 || r.errKey() != "FORBIDDEN" || r.errParam("reason") != "active_trip_limit" {
		t.Fatalf("ikkinchi safar: %d %s", r.Status, r.Raw)
	}
	// B buyurtmasi qulflanmagan va hali ochiq
	if keys := e.MR.Keys(); containsPrefix(keys, "order:accept-lock:"+b) {
		t.Errorf("rad etilgan haydovchi qulf qoldirdi")
	}
	// A tugagach B olinadi
	tid := tripA.str("id")
	d.do("POST", "/api/v1/trips/"+tid+"/start", nil)
	if c := d.do("POST", "/api/v1/trips/"+tid+"/complete", nil); c.Status != 200 {
		t.Fatal(c.Status, string(c.Raw))
	}
	if r := d.do("POST", acceptPath(offB), nil); r.Status != 200 {
		t.Fatalf("A tugagach: %d %s", r.Status, r.Raw)
	}
}

func TestTripLifecycleAndEvent(t *testing.T) {
	e := newT(t)
	d, other := e.driver(), e.driver()
	e.AddDrivers(d.ID)
	p := e.passenger()
	o := p.order(t, nil)
	id, price := o.str("id"), o.Body["price"].(float64)
	trip := d.do("POST", acceptPath(d.offerFor(t, id)), nil)
	tid := trip.str("id")

	// boshlashdan oldin tugatib bo'lmaydi
	if r := d.do("POST", "/api/v1/trips/"+tid+"/complete", nil); r.Status != 403 || r.errParam("reason") != "trip_not_started" {
		t.Errorf("tugatish: %d %s", r.Status, r.Raw)
	}
	// F-10: begona haydovchi safarni ko'rmaydi
	for _, act := range []string{"start", "complete"} {
		if r := other.do("POST", "/api/v1/trips/"+tid+"/"+act, nil); r.Status != 403 || r.errParam("reason") != "not_owner" {
			t.Errorf("begona %s: %d", act, r.Status)
		}
		if r := p.do("POST", "/api/v1/trips/"+tid+"/"+act, nil); r.Status != 403 {
			t.Errorf("yo'lovchi %s: %d", act, r.Status)
		}
	}
	e.advance(10 * time.Second)

	s := d.do("POST", "/api/v1/trips/"+tid+"/start", nil)
	if !strings.HasSuffix(s.str("started_at"), "Z") {
		t.Errorf("started_at UTC emas: %s", s.str("started_at"))
	}
	if s.Status != 200 || s.str("status") != "in_progress" || s.Body["started_at"] == nil || s.Body["completed_at"] != nil {
		t.Fatalf("start: %d %s", s.Status, s.Raw)
	}
	if g := p.do("GET", "/api/v1/orders/"+id, nil); g.str("status") != "in_progress" {
		t.Errorf("buyurtma holati: %s", g.Raw)
	}
	if r := d.do("POST", "/api/v1/trips/"+tid+"/start", nil); r.Status != 403 || r.errParam("reason") != "already_started" {
		t.Errorf("qayta start: %d %s", r.Status, r.Raw)
	}
	e.advance(10 * time.Minute)

	c := d.do("POST", "/api/v1/trips/"+tid+"/complete", nil)
	if c.Status != 200 || c.str("status") != "completed" || c.Body["final_price"] != price || c.Body["commission"].(float64) <= 0 ||
		c.Body["distance_m"].(float64) <= 0 || c.Body["completed_at"] == nil {
		t.Fatalf("complete: %d %s", c.Status, c.Raw)
	}
	if g := p.do("GET", "/api/v1/orders/"+id, nil); g.str("status") != "completed" || g.Body["price"] != price {
		t.Errorf("buyurtma: %s", g.Raw)
	}
	if r := d.do("POST", "/api/v1/trips/"+tid+"/complete", nil); r.Status != 403 || r.errParam("reason") != "trip_completed" {
		t.Errorf("qayta complete: %d %s", r.Status, r.Raw)
	}
	// hisoblagichlar bo'shadi
	if n, _ := e.Guard.Count(t.Context(), "d", d.ID); n != 0 {
		t.Errorf("haydovchi hisoblagichi %d", n)
	}
	if n, _ := e.Guard.Count(t.Context(), "p", p.ID); n != 0 {
		t.Errorf("yo'lovchi hisoblagichi %d", n)
	}

	// trip.completed (Wallet tinglaydi) — aynan bitta, kontrakt maydonlari bilan
	var evs []map[string]any
	for _, ev := range e.Outbox() {
		if ev.Subject == "izgo.trip.completed.v1" {
			var m map[string]any
			_ = json.Unmarshal(ev.Payload, &m)
			m["_id"] = ev.ID.String()
			evs = append(evs, m)
		}
	}
	if len(evs) != 1 {
		t.Fatalf("trip.completed %d marta", len(evs))
	}
	m := evs[0]
	pl := m["payload"].(map[string]any)
	if m["event"] != "trip.completed" || m["version"] != float64(1) || m["event_id"] != m["_id"] || m["occurred_at"] == nil {
		t.Errorf("envelope: %v", m)
	}
	want := map[string]any{"trip_id": tid, "order_id": id, "driver_id": d.ID.String(), "passenger_id": p.ID.String(), "final_price": price,
		"payment_method": "cash"} // Wallet naqd/elektron komissiyani shunga qarab ajratadi
	for k, v := range want {
		if pl[k] != v {
			t.Errorf("payload.%s = %v, kutilgan %v", k, pl[k], v)
		}
	}
	if pl["duration_s"] != float64(600) || pl["commission"].(float64) <= 0 || pl["distance_m"].(float64) <= 0 || pl["completed_at"] == nil || len(pl) != 10 { // 9 + payment_method
		t.Errorf("payload: %v", pl)
	}
}

func TestRating(t *testing.T) {
	e := newT(t)
	d := e.driver()
	e.AddDrivers(d.ID)
	p, other := e.passenger(), e.passenger()
	id := p.order(t, nil).str("id")
	rate := "/api/v1/orders/" + id + "/rate"

	// safar tugamaguncha baholab bo'lmaydi
	if r := p.do("POST", rate, map[string]any{"stars": 4}); r.Status != 403 || r.errParam("reason") != "order_not_completed" {
		t.Fatalf("tugamagan: %d %s", r.Status, r.Raw)
	}
	tid := d.do("POST", acceptPath(d.offerFor(t, id)), nil).str("id")
	d.do("POST", "/api/v1/trips/"+tid+"/start", nil)
	d.do("POST", "/api/v1/trips/"+tid+"/complete", nil)

	long := strings.Repeat("a", 201)
	bad := map[string]any{
		"5 yulduz, sabab yo'q":    map[string]any{"stars": 5},
		"5 yulduz, bo'sh reason":  map[string]any{"stars": 5, "reason": map[string]any{}},
		"kind noto'g'ri":          map[string]any{"stars": 5, "reason": map[string]any{"kind": "rude"}},
		"custom, matn yo'q":       map[string]any{"stars": 5, "reason": map[string]any{"kind": "custom"}},
		"custom, bo'sh matn":      map[string]any{"stars": 5, "reason": map[string]any{"kind": "custom", "custom_text": "  "}},
		"custom, 201 belgi":       map[string]any{"stars": 5, "reason": map[string]any{"kind": "custom", "custom_text": long}},
		"polite + custom_text":    map[string]any{"stars": 5, "reason": map[string]any{"kind": "polite", "custom_text": "x"}},
		"3 yulduz + reason":       map[string]any{"stars": 3, "reason": map[string]any{"kind": "polite"}},
		"3 yulduz + bo'sh reason": map[string]any{"stars": 3, "reason": map[string]any{}},
		"0 yulduz":                map[string]any{"stars": 0},
		"6 yulduz":                map[string]any{"stars": 6},
		"stars yo'q":              map[string]any{},
		"noma'lum maydon":         map[string]any{"stars": 4, "text": "x"},
	}
	for name, body := range bad {
		if r := p.do("POST", rate, body); r.Status != 422 || r.errKey() != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	// PUT baho yo'qligida
	if r := p.do("PUT", rate, map[string]any{"stars": 3}); r.Status != 404 {
		t.Errorf("PUT baho yo'q: %d %s", r.Status, r.Raw)
	}
	// egasi emas / haydovchi
	if r := other.do("POST", rate, map[string]any{"stars": 4}); r.Status != 403 || r.errParam("reason") != "not_owner" {
		t.Errorf("begona: %d", r.Status)
	}
	if r := d.do("POST", rate, map[string]any{"stars": 4}); r.Status != 403 {
		t.Errorf("haydovchi baholay olmaydi: %d", r.Status)
	}

	// 5 yulduz, har bir kind
	r := p.do("POST", rate, map[string]any{"stars": 5, "reason": map[string]any{"kind": "polite"}})
	if r.Status != 201 || r.Body["stars"] != float64(5) || r.obj("reason")["kind"] != "polite" || r.Body["updated_at"] != nil || r.str("order_id") != id {
		t.Fatalf("rate: %d %s", r.Status, r.Raw)
	}
	if r := p.do("POST", rate, map[string]any{"stars": 4}); r.Status != 403 || r.errParam("reason") != "already_rated" {
		t.Errorf("takroriy baho: %d %s", r.Status, r.Raw)
	}
	// custom matn bilan o'zgartirish
	txt := strings.Repeat("я", 200)
	u := p.do("PUT", rate, map[string]any{"stars": 5, "reason": map[string]any{"kind": "custom", "custom_text": txt}})
	if u.Status != 200 || u.obj("reason")["kind"] != "custom" || u.obj("reason")["custom_text"] != txt || u.Body["updated_at"] == nil {
		t.Fatalf("PUT custom: %d %s", u.Status, u.Raw)
	}
	// 3 yulduzga tushirish — sabab o'chadi
	u = p.do("PUT", rate, map[string]any{"stars": 3})
	if u.Status != 200 || u.Body["stars"] != float64(3) || u.Body["reason"] != nil {
		t.Fatalf("PUT 3: %d %s", u.Status, u.Raw)
	}
	if r := p.do("PUT", rate, map[string]any{"stars": 3, "reason": map[string]any{"kind": "comfort"}}); r.Status != 422 {
		t.Errorf("PUT 3 + reason: %d", r.Status)
	}
	var n int
	_ = e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM ratings WHERE order_id=$1`, uuid.MustParse(id)).Scan(&n)
	if n != 1 {
		t.Errorf("baholar soni %d", n)
	}
}

func TestOutboxRelayMarksPublished(t *testing.T) {
	e := newT(t)
	p := e.passenger()
	p.order(t, nil)
	st := e.Store
	var got []string
	n, err := st.DrainOutbox(t.Context(), 10, func(_ context.Context, ev eventT) error { got = append(got, ev.Subject); return nil })
	if err != nil || n != 1 || len(got) != 1 || got[0] != "izgo.order.created.v1" {
		t.Fatalf("drain: %d %v %v", n, got, err)
	}
	if n, _ := st.DrainOutbox(t.Context(), 10, func(context.Context, eventT) error { t.Error("qayta yuborildi"); return nil }); n != 0 {
		t.Errorf("takror: %d", n)
	}
}
