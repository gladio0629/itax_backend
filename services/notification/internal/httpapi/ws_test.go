package httpapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

func TestWebSocketReceivesOwnEventsOnly(t *testing.T) {
	e := newEnv(t)
	a, b, offline := uuid.New(), uuid.New(), uuid.New()
	e.device(a, "device-token-aaaaaaaaa", "fcm")
	e.device(offline, "device-token-offline-1", "fcm")
	ca, cb := e.mustDial(e.tok("passenger", a)), e.mustDial(e.tok("passenger", b))

	order := uuid.New()
	_ = e.svc.Handle(ctx, e.orderCreated(a, order))
	m, err := read(ca, 2*time.Second)
	if err != nil || m["type"] != "order.created" || m["id"] != order.String() || m["status"] != "searching" || m["order_id"] != order.String() {
		t.Fatalf("A xabari: %v %v", m, err)
	}
	// ulanish bor -> push YUBORILMAYDI
	if n := len(e.cap.pushed()); n != 0 {
		t.Errorf("WS ulangan bo'lsa push ketmaydi: %d", n)
	}
	// F-19: B boshqa foydalanuvchining hodisasini olmaydi
	if m, err := read(cb, 400*time.Millisecond); err == nil {
		t.Fatalf("B begona xabar oldi: %v", m)
	}
	// buyurtma holatlari ketma-ket: searching -> accepted -> completed (in_progress uchun hodisa hali yo'q)
	trip := uuid.New()
	_ = e.svc.Handle(ctx, evt("order.accepted", map[string]any{"order_id": order.String(), "trip_id": trip.String(), "driver_id": uuid.NewString(), "offer_id": uuid.NewString()}))
	_ = e.svc.Handle(ctx, evt("trip.completed", map[string]any{"trip_id": trip.String(), "order_id": order.String(), "driver_id": uuid.NewString(), "passenger_id": a.String(), "final_price": 1, "commission": 0, "distance_m": 1, "duration_s": 1}))
	var statuses []string
	for i := 0; i < 2; i++ {
		m, err := read(ca, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, m["status"])
		if m["order_id"] != order.String() {
			t.Errorf("order_id: %v", m)
		}
	}
	if statuses[0] != "accepted" || statuses[1] != "completed" {
		t.Errorf("holatlar: %v", statuses)
	}
	// ulanish yo'q foydalanuvchiga -> push
	_ = e.svc.Handle(ctx, e.orderCreated(offline, uuid.New()))
	if got := e.cap.pushed(); len(got) != 1 || got[0].Token != "device-token-offline-1" {
		t.Errorf("ulanishsiz foydalanuvchi: %+v", got)
	}
	// ulanish yopilgach yana push'ga o'tadi
	_ = ca.Close(websocket.StatusNormalClosure, "")
	time.Sleep(200 * time.Millisecond)
	_ = e.svc.Handle(ctx, e.orderCreated(a, uuid.New()))
	if got := e.cap.pushed(); len(got) != 2 || got[1].Token != "device-token-aaaaaaaaa" {
		t.Errorf("ulanish yopilgach: %+v", got)
	}
}

func TestWebSocketTokenChecks(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	sid := uuid.NewString()
	e.mr.Set("bl:sid:"+sid, "1")
	// Tokensiz so'rov endi 401 emas: birinchi xabar rejimiga o'tadi (ws_auth_test.go).
	cases := map[string]string{
		"axlat":                  "abc.def.ghi",
		"muddati o'tgan":         e.token("passenger", u, "", -time.Minute, ""),
		"admin audience":         e.token("passenger", u, "admin", time.Hour, ""),
		"rol/audience mos emas":  e.token("driver", u, "client", time.Hour, ""),
		"bekor qilingan sessiya": e.token("passenger", u, "", time.Hour, sid),
	}
	for name, tok := range cases {
		c, resp, err := e.dial(tok)
		if err == nil {
			_ = c.CloseNow()
			t.Errorf("%s: ulanish ochildi", name)
			continue
		}
		if resp == nil || resp.StatusCode != 401 {
			t.Errorf("%s: %v (HTTP %v)", name, err, resp)
		}
	}
	// to'g'ri token ochiladi
	e.mustDial(e.tok("driver", u))
}

func TestWebSocketConnectionLimit(t *testing.T) {
	e := newEnv(t)
	u, other := uuid.New(), uuid.New()
	tok := e.tok("passenger", u)
	conns := []*websocket.Conn{e.mustDial(tok), e.mustDial(tok), e.mustDial(tok)}
	c, resp, err := e.dial(tok)
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("4-ulanish ochildi (limit 3)")
	}
	if resp == nil || resp.StatusCode != 403 {
		t.Fatalf("4-ulanish: %v %v", err, resp)
	}
	// boshqa foydalanuvchiga ta'sir qilmaydi
	e.mustDial(e.tok("passenger", other))
	// bittasi yopilsa joy bo'shaydi
	_ = conns[0].Close(websocket.StatusNormalClosure, "")
	var ok bool
	for i := 0; i < 20 && !ok; i++ {
		time.Sleep(50 * time.Millisecond)
		if c, _, err := e.dial(tok); err == nil {
			ok = true
			_ = c.CloseNow()
		}
	}
	if !ok {
		t.Error("ulanish yopilgach joy bo'shamadi")
	}
	// uchta ulanishning hammasi hodisani oladi
	_ = e.svc.Handle(ctx, e.orderCreated(u, uuid.New()))
	for i, c := range conns[1:] {
		if m, err := read(c, 2*time.Second); err != nil || m["type"] != "order.created" {
			t.Errorf("ulanish %d: %v %v", i+1, m, err)
		}
	}
}

func closeCode(c *websocket.Conn, within time.Duration) (websocket.StatusCode, error) {
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil {
		return -1, nil
	}
	return websocket.CloseStatus(err), err
}

// F-19: token muddati tugaganda ulanish uziladi.
func TestWebSocketClosesWhenTokenExpires(t *testing.T) {
	e := newEnv(t)
	c := e.mustDial(e.token("passenger", uuid.New(), "", 2*time.Second, ""))
	start := time.Now()
	code, err := closeCode(c, 5*time.Second)
	if code != 4401 {
		t.Fatalf("yopish kodi %d (%v), 4401 kutilgan", code, err)
	}
	if d := time.Since(start); d < 800*time.Millisecond || d > 4*time.Second {
		t.Errorf("uzilish vaqti %v", d)
	}
}

// Identity logout qilganda (Redis blacklist) ulanish token muddatini kutmasdan uziladi.
func TestWebSocketClosesWhenSessionRevoked(t *testing.T) {
	e := newEnv(t)
	sid := uuid.NewString()
	c := e.mustDial(e.token("passenger", uuid.New(), "", time.Hour, sid))
	e.mr.Set("bl:sid:"+sid, "1")
	if code, err := closeCode(c, 3*time.Second); code != 4401 {
		t.Fatalf("yopish kodi %d (%v)", code, err)
	}
}

// Klient ma'lumot yuborolmaydi (faqat server -> klient kanal).
func TestWebSocketRejectsClientData(t *testing.T) {
	e := newEnv(t)
	c := e.mustDial(e.tok("passenger", uuid.New()))
	wctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = c.Write(wctx, websocket.MessageText, []byte(`{"subscribe":"other-user"}`))
	if code, _ := closeCode(c, 3*time.Second); code != websocket.StatusPolicyViolation {
		t.Errorf("yopish kodi %d, 1008 kutilgan", code)
	}
}

func TestSlotsReleasedAfterConnectionDrops(t *testing.T) {
	e := newEnv(t)
	u := uuid.New()
	tok := e.tok("passenger", u)
	for round := 0; round < 3; round++ { // 3 marta to'ldirib bo'shatamiz: slot sizib qolmaydi
		var cs []*websocket.Conn
		for i := 0; i < 3; i++ {
			cs = append(cs, e.mustDial(tok))
		}
		for _, c := range cs {
			_ = c.Close(websocket.StatusNormalClosure, "")
		}
		time.Sleep(300 * time.Millisecond)
	}
}
