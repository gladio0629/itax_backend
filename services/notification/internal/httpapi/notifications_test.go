package httpapi_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/notification/internal/events"
)

var ctx = context.Background()

// Barcha hodisalar: to'g'ri qabul qiluvchi, ro'yxatga yozish va push (WebSocket yo'q) — F-21: faqat {type, id}.
func TestEventsStoreAndPushMinimal(t *testing.T) {
	e := newEnv(t)
	passenger, driver := uuid.New(), uuid.New()
	order, trip, offer, wallet := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	e.device(passenger, "passenger-device-token-1", "fcm")
	e.device(driver, "driver-device-token-1", "apns")
	s := func(u uuid.UUID) string { return u.String() }

	steps := []struct {
		name string
		raw  []byte
		user uuid.UUID
		id   uuid.UUID
		typ  string
		dev  string
	}{
		{"order.created", e.orderCreated(passenger, order), passenger, order, "order.created", "passenger-device-token-1"},
		{"offer.created", evt("offer.created", map[string]any{"offer_id": s(offer), "order_id": s(order), "driver_id": s(driver), "eta_s": 300}), driver, offer, "offer.created", "driver-device-token-1"},
		{"order.accepted", evt("order.accepted", map[string]any{"order_id": s(order), "trip_id": s(trip), "driver_id": s(driver), "offer_id": s(offer)}), passenger, order, "order.accepted", "passenger-device-token-1"},
		{"trip.completed", evt("trip.completed", map[string]any{"trip_id": s(trip), "order_id": s(order), "driver_id": s(driver), "passenger_id": s(passenger), "final_price": 150000, "commission": 15000, "distance_m": 4615, "duration_s": 600}), passenger, trip, "trip.completed", "passenger-device-token-1"},
		{"wallet.limit_reached", evt("wallet.limit_reached", map[string]any{"wallet_id": s(wallet), "driver_id": s(driver), "balance": -5000000, "limit": -5000000}), driver, wallet, "wallet.limit_reached", "driver-device-token-1"},
		{"driver.approved", evt("driver.approved", map[string]any{"driver_id": s(driver), "approved_by": s(uuid.New())}), driver, driver, "driver.approved", "driver-device-token-1"},
	}
	for i, st := range steps {
		if err := e.svc.Handle(ctx, st.raw); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		got := e.cap.pushed()
		if len(got) != i+1 {
			t.Fatalf("%s: %d ta push, kutilgan %d", st.name, len(got), i+1)
		}
		p := got[i]
		if p.Token != st.dev || len(p.Data) != 2 || p.Data["type"] != st.typ || p.Data["id"] != s(st.id) {
			t.Errorf("%s: push %+v", st.name, p)
		}
		e.advance(time.Second)
	}
	// Hech bir push'da narx, manzil, ism kabi tafsilot yo'q
	for _, p := range e.cap.pushed() {
		for _, v := range p.Data {
			if strings.Contains(v, "Maxfiy") || strings.Contains(v, "99999") {
				t.Errorf("push'da tafsilot: %v", p.Data)
			}
		}
	}
	// Ro'yxatlar: har kim faqat o'zinikini ko'radi
	pl := e.list(e.tok("passenger", passenger), "")
	dl := e.list(e.tok("driver", driver), "")
	if len(ids(pl)) != 3 || len(ids(dl)) != 3 {
		t.Fatalf("yo'lovchi %d, haydovchi %d ta (3/3 kutilgan): %s / %s", len(ids(pl)), len(ids(dl)), pl.Raw, dl.Raw)
	}
	first := pl.Body["data"].([]any)[0].(map[string]any) // eng yangisi: trip.completed
	if first["kind"] != "trip" || first["read"] != false || first["title_key"] != "notification.trip.completed.title" {
		t.Errorf("birinchi: %v", first)
	}
	if strings.Contains(string(pl.Raw), "99999") || strings.Contains(string(pl.Raw), "Maxfiy") || strings.Contains(string(pl.Raw), "150000") {
		t.Errorf("ro'yxatda tafsilot: %s", pl.Raw)
	}
	for k, v := range first["params"].(map[string]any) {
		if _, err := uuid.Parse(v.(string)); err != nil {
			t.Errorf("params[%s]=%v ID emas", k, v)
		}
	}
}

func TestAcceptedBeforeCreatedIsRetried(t *testing.T) {
	e := newEnv(t)
	passenger, order := uuid.New(), uuid.New()
	e.device(passenger, "passenger-device-token-1", "fcm")
	acc := evt("order.accepted", map[string]any{"order_id": order.String(), "trip_id": uuid.NewString(), "driver_id": uuid.NewString(), "offer_id": uuid.NewString()})
	if err := e.svc.Handle(ctx, acc); err == nil || !errorsIs(err, events.ErrNoContext) {
		t.Fatalf("yo'lovchi noma'lum: %v", err)
	}
	if len(e.cap.pushed()) != 0 {
		t.Fatal("qabul qiluvchisiz push yuborildi")
	}
	_ = e.svc.Handle(ctx, e.orderCreated(passenger, order))
	if err := e.svc.Handle(ctx, acc); err != nil { // qayta urinish endi o'tadi
		t.Fatal(err)
	}
	if got := e.cap.pushed(); len(got) != 2 || got[1].Data["type"] != "order.accepted" {
		t.Fatalf("%+v", got)
	}
}

func errorsIs(err, target error) bool { return strings.Contains(err.Error(), target.Error()) }

func TestRedeliveryIsIdempotent(t *testing.T) {
	e := newEnv(t)
	p, o := uuid.New(), uuid.New()
	e.device(p, "passenger-device-token-1", "fcm")
	raw := e.orderCreated(p, o)
	for i := 0; i < 3; i++ {
		if err := e.svc.Handle(ctx, raw); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.cap.pushed()); n != 1 {
		t.Errorf("%d ta push", n)
	}
	if n := len(ids(e.list(e.tok("passenger", p), ""))); n != 1 {
		t.Errorf("%d ta bildirishnoma", n)
	}
	// buzuq hodisa -> ErrPoison
	if err := e.svc.Handle(ctx, []byte(`{`)); !errorsIs(err, events.ErrPoison) {
		t.Errorf("poison: %v", err)
	}
}

func TestUnregisteredDeviceIsRemoved(t *testing.T) {
	e := newEnv(t)
	p := uuid.New()
	e.device(p, "dead-token-0000000001", "fcm")
	e.device(p, "live-token-0000000001", "fcm")
	e.cap.unreg["dead-token-0000000001"] = true
	_ = e.svc.Handle(ctx, e.orderCreated(p, uuid.New()))
	devs, _ := e.st.Devices(ctx, p)
	if len(devs) != 1 || devs[0].Token != "live-token-0000000001" {
		t.Fatalf("qurilmalar: %+v", devs)
	}
	if len(e.cap.pushed()) != 1 {
		t.Errorf("push soni %d", len(e.cap.pushed()))
	}
}

func TestListPaginationReadAndTTL(t *testing.T) {
	e := newEnv(t)
	u, other := uuid.New(), uuid.New()
	tok := e.tok("passenger", u)
	var want []string
	for i := 0; i < 7; i++ {
		if i != 3 && i != 4 { // 3 va 4 bir xil millisekundda (kursor bir xil ball)
			e.advance(5 * time.Millisecond)
		}
		_ = e.svc.Handle(ctx, e.orderCreated(u, uuid.New()))
	}
	all := e.list(tok, "?limit=100")
	want = ids(all)
	if len(want) != 7 {
		t.Fatalf("%d ta", len(want))
	}
	var got []string
	cursor := ""
	for page := 0; page < 10; page++ {
		q := "?limit=3"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		r := e.list(tok, q)
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Raw)
		}
		got = append(got, ids(r)...)
		if r.Body["cursor"] == nil {
			break
		}
		cursor = r.Body["cursor"].(string)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("sahifalash:\n got  %v\n want %v", got, want)
	}
	for _, q := range []string{"?limit=0", "?limit=101", "?limit=x", "?cursor=@@", "?cursor=1.notuuid"} {
		if r := e.list(tok, q); r.Status != 422 {
			t.Errorf("%s: %d", q, r.Status)
		}
	}

	// o'qilgan deb belgilash
	target := want[2]
	if r := e.do("PUT", "/api/v1/notifications/"+target+"/read", bearer(tok), nil); r.Status != 204 {
		t.Fatalf("read: %d %s", r.Status, r.Raw)
	}
	if r := e.do("PUT", "/api/v1/notifications/"+target+"/read", bearer(tok), nil); r.Status != 204 {
		t.Errorf("takroriy read: %d", r.Status)
	}
	for _, it := range e.list(tok, "?limit=100").Body["data"].([]any) {
		m := it.(map[string]any)
		if (m["id"] == target) != (m["read"] == true) {
			t.Errorf("read bayrog'i: %v", m)
		}
	}
	// TTL saqlanib qolgan (KEEPTTL)
	if ttl := e.mr.TTL("notif:" + u.String() + ":" + target); ttl <= 29*24*time.Hour {
		t.Errorf("read TTL ni buzdi: %v", ttl)
	}
	// boshqa foydalanuvchi / noto'g'ri ID / tokensiz
	if r := e.do("PUT", "/api/v1/notifications/"+target+"/read", bearer(e.tok("passenger", other)), nil); r.Status != 404 {
		t.Errorf("begona: %d", r.Status)
	}
	if r := e.do("PUT", "/api/v1/notifications/"+uuid.NewString()+"/read", bearer(tok), nil); r.Status != 404 {
		t.Errorf("mavjud emas: %d", r.Status)
	}
	if r := e.do("PUT", "/api/v1/notifications/zzz/read", bearer(tok), nil); r.Status != 404 {
		t.Errorf("noto'g'ri id: %d", r.Status)
	}
	if r := e.do("GET", "/api/v1/notifications", nil, nil); r.Status != 401 || r.errKey() != "AUTH_TOKEN_EXPIRED" {
		t.Errorf("tokensiz: %d %s", r.Status, r.Raw)
	}
	if n := len(ids(e.list(e.tok("passenger", other), ""))); n != 0 {
		t.Errorf("begona foydalanuvchi %d ta ko'rdi", n)
	}

	// 30 kundan keyin yozuvlar yo'q (Redis TTL)
	e.mr.FastForward(30*24*time.Hour + time.Second)
	if n := len(ids(e.list(tok, ""))); n != 0 {
		t.Errorf("30 kundan keyin %d ta qoldi", n)
	}
}

func TestPerUserCap(t *testing.T) {
	e := newEnv(t, func(o *opts) { o.maxPerUser = 3 })
	u := uuid.New()
	for i := 0; i < 6; i++ {
		e.advance(time.Millisecond)
		_ = e.svc.Handle(ctx, e.orderCreated(u, uuid.New()))
	}
	if n := len(ids(e.list(e.tok("passenger", u), "?limit=100"))); n != 3 {
		t.Errorf("%d ta, kutilgan 3 (eng yangilari)", n)
	}
}
