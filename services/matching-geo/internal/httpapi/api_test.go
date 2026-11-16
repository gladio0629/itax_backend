package httpapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/izgo/shared/go/authn"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/izgo/matching-geo/internal/events"
	"github.com/izgo/matching-geo/internal/httpapi"
	"github.com/izgo/matching-geo/internal/service"
	"github.com/izgo/matching-geo/internal/store"
	"github.com/izgo/shared/geo"
	"github.com/redis/go-redis/v9"
)

const token = "test-internal-token-0123456789"

// capturePub — offer.created ni NATS o'rniga yig'adi.
type capturePub struct {
	mu    sync.Mutex
	got   []events.OfferCreated
	trace []string
	fail  bool
	calls int
}

func (p *capturePub) PublishOfferCreated(_ context.Context, o events.OfferCreated, trace string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.fail {
		return errors.New("nats yo'q")
	}
	p.got = append(p.got, o)
	p.trace = append(p.trace, trace)
	return nil
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *logBuf) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

type env struct {
	t    *testing.T
	mr   *miniredis.Miniredis
	rdb  *redis.Client
	svc  *service.Service
	pub  *capturePub
	clk  *clock
	logs *logBuf
	srv  *httptest.Server
	// jwtKey — testdagi "Identity" private kaliti (public API tokenlarini imzolash uchun)
	jwtKey ed25519.PrivateKey
}

const (
	baseLat = 41.3111
	baseLng = 69.2797
)

// newEnv — opts: store/service sozlamalarini o'zgartirish (masalan, F-11 StationaryAfter).
func newEnv(t *testing.T, opts ...func(*store.Options, *service.Options)) *env {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	clk := &clock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	logs := &logBuf{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	router, _ := geo.NewHaversineRouter(1.3, 30)
	pub := &capturePub{}
	stOpt := store.Options{LocationTTL: 2 * time.Minute, BusyTTL: 24 * time.Hour}
	svcOpt := service.Options{
		MaxSpeedKMH: 300, MinJumpM: 200, DefaultRadiusM: 50_000, MaxRadiusM: 200_000, MaxCandidates: 500, OfferTTL: 2 * time.Minute,
	}
	for _, o := range opts {
		o(&stOpt, &svcOpt)
	}
	st := store.New(rdb, stOpt)
	svc := service.New(st, router, pub, svcOpt, log).WithClock(clk.Now)
	jwtPub, jwtPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verifier := authn.NewVerifier(jwtPub, jwtIssuer, rdb)
	srv := httptest.NewServer(httpapi.New(svc, []string{token, "old-rotating-token-9876543210"}, verifier, 200_000, log).Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, mr: mr, rdb: rdb, svc: svc, pub: pub, clk: clk, logs: logs, srv: srv, jwtKey: jwtPriv}
}

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

func (r resp) drivers() []map[string]any {
	var out []map[string]any
	for _, d := range r.Body["drivers"].([]any) {
		out = append(out, d.(map[string]any))
	}
	return out
}

func (e *env) do(method, path, tok string, body any) resp {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewBufferString(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("X-Internal-Token", tok)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: raw}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.Body); err != nil {
			e.t.Fatalf("JSON emas (%d): %q", res.StatusCode, raw)
		}
	}
	return out
}

func (e *env) call(method, path string, body any) resp { return e.do(method, path, token, body) }

// north — baza nuqtadan km kilometr shimolda
func north(km float64) float64 { return baseLat + km/111.195 }

func (e *env) location(id uuid.UUID, lat, lng float64) resp {
	e.t.Helper()
	return e.call("POST", "/internal/matching/location", map[string]any{"driver_id": id.String(), "lat": lat, "lng": lng})
}

func (e *env) profile(id uuid.UUID, p map[string]any) {
	e.t.Helper()
	if r := e.call("PUT", "/internal/matching/drivers/"+id.String()+"/profile", p); r.Status != 204 {
		e.t.Fatalf("profile: %d %s", r.Status, r.Raw)
	}
}

// driver — joylashuv + faol profil.
func (e *env) driver(km float64, extra map[string]any) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	p := map[string]any{"state": "active", "seats": 4, "car_class": "economy"}
	for k, v := range extra {
		if v == nil {
			delete(p, k)
		} else {
			p[k] = v
		}
	}
	e.profile(id, p)
	if r := e.location(id, north(km), baseLng); r.Status != 200 {
		e.t.Fatalf("location: %d %s", r.Status, r.Raw)
	}
	return id
}

func findReq(extra map[string]any) map[string]any {
	b := map[string]any{"type": "passenger", "seats": 1, "from": map[string]any{"lat": baseLat, "lng": baseLng}}
	for k, v := range extra {
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
	}
	return b
}

func (e *env) findIDs(extra map[string]any) []string {
	e.t.Helper()
	r := e.call("POST", "/internal/matching/find", findReq(extra))
	if r.Status != 200 {
		e.t.Fatalf("find: %d %s", r.Status, r.Raw)
	}
	var ids []string
	for _, d := range r.drivers() {
		ids = append(ids, d["driver_id"].(string))
	}
	return ids
}

func same(t *testing.T, name string, got []string, want ...uuid.UUID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %v, kutilgan %v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i].String() {
			t.Fatalf("%s: %d-o'rin %s, kutilgan %s", name, i, got[i], want[i])
		}
	}
}

// ---------------------------------------------------------------- testlar

func TestInternalTokenRequiredEverywhere(t *testing.T) {
	e := newEnv(t)
	id := uuid.NewString()
	endpoints := []struct{ method, path string }{
		{"POST", "/internal/matching/location"}, {"POST", "/internal/matching/find"}, {"POST", "/internal/matching/route"},
		{"PUT", "/internal/matching/drivers/" + id + "/profile"}, {"POST", "/internal/matching/drivers/" + id + "/profile"},
		{"PUT", "/internal/matching/drivers/" + id + "/status"},
		{"GET", "/internal/matching/unknown"},
	}
	for _, ep := range endpoints {
		for name, tok := range map[string]string{"yo'q": "", "noto'g'ri": "wrong-token-0123456789", "prefiks": token[:12], "uzun": token + "x"} {
			if r := e.do(ep.method, ep.path, tok, `{}`); r.Status != 403 || r.errKey() != "FORBIDDEN" {
				t.Errorf("%s %s [%s]: %d %s", ep.method, ep.path, name, r.Status, r.Raw)
			}
		}
	}
	// almashtirish davrida ikkala token ishlaydi; healthz ochiq
	if r := e.do("POST", "/internal/matching/route", "old-rotating-token-9876543210", map[string]any{"from": map[string]any{"lat": 1, "lng": 1}, "to": map[string]any{"lat": 1, "lng": 2}}); r.Status != 200 {
		t.Errorf("eski token: %d", r.Status)
	}
	if res, err := http.Get(e.srv.URL + "/healthz"); err != nil || res.StatusCode != 200 {
		t.Errorf("healthz: %v %v", res, err)
	}
	if r := e.call("GET", "/internal/matching/unknown", nil); r.Status != 404 {
		t.Errorf("noma'lum yo'l: %d", r.Status)
	}
}

func TestLocationUpdateAndValidation(t *testing.T) {
	e := newEnv(t)
	id := uuid.New()
	r := e.location(id, 41.3, 69.2)
	if r.Status != 200 || r.Body["suspicious"] != false {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	// Redis GEO'da saqlangan, TTL 2 daqiqa
	pos, err := e.rdb.GeoPos(context.Background(), "drivers:geo", id.String()).Result()
	if err != nil || pos[0] == nil || pos[0].Latitude < 41.299 || pos[0].Latitude > 41.301 {
		t.Fatalf("geopos: %v %v", pos, err)
	}
	if ttl := e.mr.TTL("driver:" + id.String() + ":loc"); ttl != 2*time.Minute {
		t.Errorf("TTL = %v", ttl)
	}
	bad := map[string]any{
		"driver_id yo'q":      map[string]any{"lat": 1, "lng": 1},
		"driver_id noto'g'ri": map[string]any{"driver_id": "x", "lat": 1, "lng": 1},
		"lat yo'q":            map[string]any{"driver_id": id.String(), "lng": 1},
		"lat > 90":            map[string]any{"driver_id": id.String(), "lat": 90.5, "lng": 1},
		"lng < -180":          map[string]any{"driver_id": id.String(), "lat": 1, "lng": -181},
		"noma'lum maydon":     map[string]any{"driver_id": id.String(), "lat": 1, "lng": 1, "speed": 5},
		"satr lat":            `{"driver_id":"` + id.String() + `","lat":"1","lng":1}`,
		"buzuq":               `{"driver_id":`,
	}
	for name, body := range bad {
		if r := e.call("POST", "/internal/matching/location", body); r.Status != 422 || r.errKey() != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
}

// F-11: tezlik sakrashi signal beradi (log + stream + javob flagi).
func TestSpeedJumpSignal(t *testing.T) {
	e := newEnv(t)
	id := uuid.New()
	e.location(id, north(0), baseLng)

	// 10 soniyadan keyin 278 m (100 km/s) — normal
	e.clk.Advance(10 * time.Second)
	if r := e.location(id, north(0.278), baseLng); r.Body["suspicious"] != false {
		t.Fatalf("oddiy haydash: %s", r.Raw)
	}
	if e.logs.String() != "" && contains(e.logs.String(), "F-11") {
		t.Fatal("oddiy haydashda signal berildi")
	}
	n, _ := e.rdb.XLen(context.Background(), "matching:gps-anomalies").Result()
	if n != 0 {
		t.Fatalf("anomaliya yozuvlari %d", n)
	}

	// 10 soniyada 5 km (~1800 km/s) — signal
	e.clk.Advance(10 * time.Second)
	r := e.location(id, north(5.278), baseLng)
	if r.Status != 200 || r.Body["suspicious"] != true {
		t.Fatalf("sakrash: %d %s", r.Status, r.Raw)
	}
	if !contains(e.logs.String(), "F-11") || !contains(e.logs.String(), id.String()) {
		t.Errorf("WARN log yo'q: %s", e.logs.String())
	}
	msgs, _ := e.rdb.XRange(context.Background(), "matching:gps-anomalies", "-", "+").Result()
	if len(msgs) != 1 || msgs[0].Values["driver_id"] != id.String() || msgs[0].Values["kind"] != "speed_jump" {
		t.Fatalf("stream: %+v", msgs)
	}
	// nuqta baribir saqlandi (signal-only): qidiruvda yangi joyda topiladi
	e.profile(id, map[string]any{"state": "active", "seats": 4})
	far := e.findIDs(map[string]any{"from": map[string]any{"lat": north(5.278), "lng": baseLng}, "radius_m": 1000})
	if len(far) != 1 || far[0] != id.String() {
		t.Errorf("yangi nuqta saqlanmadi: %v", far)
	}

	// 2 daqiqa jim turib, keyin boshqa joyda paydo bo'lsa — oldingi nuqta yo'q (TTL), signal yo'q
	e.clk.Advance(3 * time.Minute)
	e.mr.FastForward(3 * time.Minute)
	if r := e.location(id, north(500), baseLng); r.Body["suspicious"] != false {
		t.Errorf("TTL dan keyin: %s", r.Raw)
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }

func TestFindRadiusSortAndShape(t *testing.T) {
	e := newEnv(t)
	d10 := e.driver(10, nil)
	d1 := e.driver(1, nil)
	d30 := e.driver(30, nil)
	d60 := e.driver(60, nil) // default 50 km dan tashqarida

	r := e.call("POST", "/internal/matching/find", findReq(nil))
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	got := r.drivers()
	same(t, "default radius 50 km", []string{got[0]["driver_id"].(string), got[1]["driver_id"].(string), got[2]["driver_id"].(string)}, d1, d10, d30)
	if len(got) != 3 {
		t.Fatalf("%d ta natija", len(got))
	}
	first := got[0]
	loc := first["location"].(map[string]any)
	dist := first["distance_m"].(float64)
	if dist < 990 || dist > 1010 || loc["lat"].(float64) < north(1)-1e-3 || loc["lat"].(float64) > north(1)+1e-3 ||
		first["pickup_eta_s"].(float64) <= 0 {
		t.Errorf("birinchi natija: %v", first)
	}
	if _, has := first["offer_id"]; has {
		t.Error("order_id yo'q so'rovda offer_id bo'lmasligi kerak")
	}
	if len(e.pub.got) != 0 {
		t.Error("order_id yo'q so'rov offer.created chiqardi")
	}

	same(t, "radius_m=5000", e.findIDs(map[string]any{"radius_m": 5000}), d1)
	same(t, "radius_m=100 km", e.findIDs(map[string]any{"radius_m": 100_000}), d1, d10, d30, d60)
	same(t, "limit=2", e.findIDs(map[string]any{"radius_m": 100_000, "limit": 2}), d1, d10)
	if r := e.call("POST", "/internal/matching/find", findReq(map[string]any{"radius_m": 500})); len(r.drivers()) != 0 {
		t.Errorf("500 m: %s", r.Raw)
	} else if string(bytes.TrimSpace(r.Raw)) != `{"drivers":[]}` {
		t.Errorf("bo'sh ro'yxat [] bo'lishi kerak (null emas): %s", r.Raw)
	}
}

func TestFindFiltersByOrderType(t *testing.T) {
	e := newEnv(t)
	sedan := e.driver(1, map[string]any{"seats": 4, "car_class": "economy"})
	small := e.driver(2, map[string]any{"seats": 2, "car_class": "business"})
	cargo := e.driver(3, map[string]any{"seats": nil, "car_class": nil, "vehicle_type": "labo"})
	e.driver(4, map[string]any{"state": "blocked"})
	e.driver(5, map[string]any{"state": "pending_review"})

	same(t, "seats=1", e.findIDs(map[string]any{"seats": 1}), sedan, small)
	same(t, "seats=3", e.findIDs(map[string]any{"seats": 3}), sedan)
	same(t, "seats=4", e.findIDs(map[string]any{"seats": 4}), sedan)
	same(t, "car_class=business", e.findIDs(map[string]any{"seats": nil, "car_class": "business"}), small)
	same(t, "delivery labo", e.findIDs(map[string]any{"type": "delivery", "seats": nil, "vehicle_type": "labo"}), cargo)
	same(t, "delivery fura", e.findIDs(map[string]any{"type": "delivery", "seats": nil, "vehicle_type": "fura"}))
}

func TestFindValidation(t *testing.T) {
	e := newEnv(t)
	bad := map[string]map[string]any{
		"type yo'q":             findReq(map[string]any{"type": nil}),
		"type noto'g'ri":        findReq(map[string]any{"type": "taxi"}),
		"seats/class yo'q":      findReq(map[string]any{"seats": nil}),
		"seats 0":               findReq(map[string]any{"seats": 0}),
		"seats 5":               findReq(map[string]any{"seats": 5}),
		"seats + car_class":     findReq(map[string]any{"car_class": "economy"}),
		"passenger + vehicle":   findReq(map[string]any{"vehicle_type": "labo"}),
		"car_class noto'g'ri":   findReq(map[string]any{"seats": nil, "car_class": "vip"}),
		"delivery vehicle yo'q": findReq(map[string]any{"type": "delivery", "seats": nil}),
		"delivery + seats":      findReq(map[string]any{"type": "delivery", "vehicle_type": "labo"}),
		"vehicle noto'g'ri":     findReq(map[string]any{"type": "delivery", "seats": nil, "vehicle_type": "bus"}),
		"from yo'q":             findReq(map[string]any{"from": nil}),
		"from lat":              findReq(map[string]any{"from": map[string]any{"lat": 100, "lng": 1}}),
		"radius 0":              findReq(map[string]any{"radius_m": 0}),
		"radius > max":          findReq(map[string]any{"radius_m": 200_001}),
		"limit 0":               findReq(map[string]any{"limit": 0}),
		"limit 101":             findReq(map[string]any{"limit": 101}),
		"order_id noto'g'ri":    findReq(map[string]any{"order_id": "x"}),
		"expires order_idsiz":   findReq(map[string]any{"expires_at": e.clk.Now().Add(time.Hour).Format(time.RFC3339)}),
		"narx yuborish":         findReq(map[string]any{"price": 1}),
	}
	for name, body := range bad {
		if r := e.call("POST", "/internal/matching/find", body); r.Status != 422 || r.errKey() != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	// expires_at tekshiruvlari (order_id bilan)
	now := time.Now()
	for name, at := range map[string]string{"o'tmish": now.Add(-time.Hour).Format(time.RFC3339), "juda uzoq": now.Add(48 * time.Hour).Format(time.RFC3339), "format": "ertaga"} {
		body := findReq(map[string]any{"order_id": uuid.NewString(), "expires_at": at})
		if r := e.call("POST", "/internal/matching/find", body); r.Status != 422 {
			t.Errorf("expires_at %s: %d %s", name, r.Status, r.Raw)
		}
	}
}

// order_id berilganda: deterministik offer_id va offer.created (shared/events/offer.created.json bo'yicha).
func TestFindCreatesOffersAndEvents(t *testing.T) {
	e := newEnv(t)
	d1, d2 := e.driver(1, nil), e.driver(2, nil)
	order := uuid.New()

	exp := time.Now().Add(90 * time.Second).UTC().Truncate(time.Second)
	body := findReq(map[string]any{"order_id": order.String(), "expires_at": exp.Format(time.RFC3339)})
	r := e.call("POST", "/internal/matching/find", body)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	ds := r.drivers()
	if len(ds) != 2 || ds[0]["offer_id"] != service.OfferID(order, d1).String() || ds[1]["offer_id"] != service.OfferID(order, d2).String() ||
		ds[0]["offer_expires_at"] != exp.Format(time.RFC3339) {
		t.Fatalf("offer_id/expiry: %s", r.Raw)
	}
	if len(e.pub.got) != 2 {
		t.Fatalf("%d ta offer.created", len(e.pub.got))
	}
	for i, o := range e.pub.got {
		d := ds[i]
		if o.OfferID.String() != d["offer_id"] || o.OrderID != order || o.DriverID.String() != d["driver_id"] ||
			o.ETAs != int(d["pickup_eta_s"].(float64)) || !o.ExpiresAt.Equal(exp) || e.pub.trace[i] == "" {
			t.Errorf("hodisa %d: %+v (javob %v)", i, o, d)
		}
	}
	// takroriy chaqiruv (Trip & Order qayta urindi): offer_id'lar o'zgarmaydi
	r2 := e.call("POST", "/internal/matching/find", body)
	if r2.drivers()[0]["offer_id"] != ds[0]["offer_id"] {
		t.Error("offer_id deterministik emas")
	}
	// boshqa buyurtma -> boshqa offer_id
	if service.OfferID(uuid.New(), d1) == service.OfferID(order, d1) {
		t.Error("offer_id buyurtmaga bog'liq emas")
	}
	// expires_at berilmasa: hozir + OFFER_TTL (2 daqiqa)
	e.pub.got = nil
	r3 := e.call("POST", "/internal/matching/find", findReq(map[string]any{"order_id": uuid.NewString()}))
	want := e.clk.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	if r3.drivers()[0]["offer_expires_at"] != want {
		t.Errorf("default muddat: %v, kutilgan %s", r3.drivers()[0]["offer_expires_at"], want)
	}
}

// NATS ishlamasa qidiruv natijasi baribir qaytadi (hodisa log'ga yoziladi, qolganlari urinilmaydi).
func TestFindSurvivesPublishFailure(t *testing.T) {
	e := newEnv(t)
	e.driver(1, nil)
	e.driver(2, nil)
	e.pub.fail = true
	r := e.call("POST", "/internal/matching/find", findReq(map[string]any{"order_id": uuid.NewString()}))
	if r.Status != 200 || len(r.drivers()) != 2 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if e.pub.calls != 1 {
		t.Errorf("birinchi xatodan keyin to'xtash kerak, urinishlar: %d", e.pub.calls)
	}
	if !contains(e.logs.String(), "offer.created yuborilmadi") {
		t.Errorf("xato log'ga yozilmadi: %s", e.logs.String())
	}
}

func tripEvent(event string, driver, trip uuid.UUID) []byte {
	b, _ := json.Marshal(map[string]any{"event": event, "version": 1, "event_id": uuid.NewString(), "occurred_at": "2026-11-12T10:00:00Z",
		"payload": map[string]any{"driver_id": driver.String(), "trip_id": trip.String(), "order_id": uuid.NewString()}})
	return b
}

// order.accepted -> busy (takliflardan chiqadi), trip.completed -> available (qaytadi).
func TestStatusFollowsTripEvents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d, other := e.driver(1, nil), e.driver(2, nil)
	trip := uuid.New()
	same(t, "boshlanishda", e.findIDs(nil), d, other)

	if err := e.svc.OnOrderAccepted(ctx, tripEvent("order.accepted", d, trip)); err != nil {
		t.Fatal(err)
	}
	same(t, "order.accepted dan keyin", e.findIDs(nil), other)
	if v, _ := e.rdb.Get(ctx, "driver:"+d.String()+":status").Result(); v != "busy" {
		t.Errorf("driver:{id}:status = %q", v)
	}
	// takroriy yetkazish zarar qilmaydi
	if err := e.svc.OnOrderAccepted(ctx, tripEvent("order.accepted", d, trip)); err != nil {
		t.Fatal(err)
	}
	same(t, "takroriy accepted", e.findIDs(nil), other)

	if err := e.svc.OnTripCompleted(ctx, tripEvent("trip.completed", d, trip)); err != nil {
		t.Fatal(err)
	}
	same(t, "trip.completed dan keyin", e.findIDs(nil), d, other)
	if v, _ := e.rdb.Get(ctx, "driver:"+d.String()+":status").Result(); v != "available" {
		t.Errorf("status = %q", v)
	}
	// kech kelgan takroriy accepted tugagan safar uchun haydovchini qayta band qilmaydi
	if err := e.svc.OnOrderAccepted(ctx, tripEvent("order.accepted", d, trip)); err != nil {
		t.Fatal(err)
	}
	same(t, "kech accepted", e.findIDs(nil), d, other)

	// buzuq hodisalar tashlanadi (ErrPoison), holat o'zgarmaydi
	for name, raw := range map[string][]byte{"json": []byte(`{`), "boshqa hodisa": tripEvent("trip.completed", d, uuid.New())} {
		if err := e.svc.OnOrderAccepted(ctx, raw); !errors.Is(err, events.ErrPoison) {
			t.Errorf("%s: %v", name, err)
		}
	}
	same(t, "buzuq hodisalardan keyin", e.findIDs(nil), d, other)
}

func TestStatusEndpoint(t *testing.T) {
	e := newEnv(t)
	d := e.driver(1, nil)
	path := "/internal/matching/drivers/" + d.String() + "/status"

	if r := e.call("PUT", path, map[string]any{"status": "offline"}); r.Status != 200 || r.Body["status"] != "offline" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	same(t, "offline", e.findIDs(nil))
	// offline holatdagi haydovchi joylashuv yuborsa ham taklif olmaydi
	e.location(d, north(1), baseLng)
	same(t, "offline + joylashuv", e.findIDs(nil))
	if r := e.call("PUT", path, map[string]any{"status": "available"}); r.Status != 200 {
		t.Fatalf("%d", r.Status)
	}
	same(t, "available", e.findIDs(nil), d)

	// busy ni qo'lda o'rnatib bo'lmaydi; band haydovchini qo'lda available qilib bo'lmaydi
	if r := e.call("PUT", path, map[string]any{"status": "busy"}); r.Status != 422 {
		t.Errorf("busy qo'lda: %d", r.Status)
	}
	_ = e.svc.OnOrderAccepted(context.Background(), tripEvent("order.accepted", d, uuid.New()))
	if r := e.call("PUT", path, map[string]any{"status": "available"}); r.Status != 403 || r.Body["error"].(map[string]any)["params"].(map[string]any)["reason"] != "driver_busy" {
		t.Errorf("band haydovchi: %d %s", r.Status, r.Raw)
	}
	for _, body := range []any{map[string]any{}, map[string]any{"status": "x"}, map[string]any{"status": "available", "extra": 1}} {
		if r := e.call("PUT", path, body); r.Status != 422 {
			t.Errorf("%v: %d", body, r.Status)
		}
	}
	if r := e.call("PUT", "/internal/matching/drivers/not-a-uuid/status", map[string]any{"status": "offline"}); r.Status != 422 {
		t.Errorf("noto'g'ri id: %d", r.Status)
	}
}

func TestProfileEndpoint(t *testing.T) {
	e := newEnv(t)
	d := e.driver(1, nil)
	path := "/internal/matching/drivers/" + d.String() + "/profile"
	same(t, "active", e.findIDs(nil), d)

	// bloklangan -> takliflardan chiqadi; PUT to'liq almashtiradi
	e.profile(d, map[string]any{"state": "blocked", "seats": 4})
	same(t, "blocked", e.findIDs(nil))
	e.profile(d, map[string]any{"state": "active"}) // sig'im yo'q -> yo'lovchi tashimaydi
	same(t, "seats yo'q", e.findIDs(nil))
	e.profile(d, map[string]any{"state": "active", "seats": 3})
	same(t, "seats=3", e.findIDs(map[string]any{"seats": 3}), d)

	for name, body := range map[string]any{
		"state yo'q": map[string]any{"seats": 4}, "state noto'g'ri": map[string]any{"state": "online"},
		"seats 0": map[string]any{"state": "active", "seats": 0}, "seats 9": map[string]any{"state": "active", "seats": 9},
		"car_class": map[string]any{"state": "active", "car_class": "vip"}, "vehicle": map[string]any{"state": "active", "vehicle_type": "bus"},
		"noma'lum": map[string]any{"state": "active", "rating": 5},
	} {
		if r := e.call("PUT", path, body); r.Status != 422 {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
}

// POST — Driver servisi sinxronlashi: camelCase body, status = Driver holati, ixtiyoriy joylashuv.
func TestSyncProfileEndpoint(t *testing.T) {
	e := newEnv(t)
	d := uuid.New()
	path := "/internal/matching/drivers/" + d.String() + "/profile"
	body := func(extra map[string]any) map[string]any {
		b := map[string]any{"driverId": d.String(), "status": "active", "seats": 4, "carClass": "comfort", "vehicleType": nil, "location": nil}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}

	// joylashuvsiz profil — hali mos emas
	if r := e.call("POST", path, body(nil)); r.Status != 204 {
		t.Fatalf("sync: %d %s", r.Status, r.Raw)
	}
	same(t, "joylashuv yo'q", e.findIDs(nil))

	// location bilan — darhol mos keladi (seats va car_class bo'yicha)
	loc := map[string]any{"lat": north(1), "lng": baseLng}
	if r := e.call("POST", path, body(map[string]any{"location": loc})); r.Status != 204 {
		t.Fatalf("sync+location: %d %s", r.Status, r.Raw)
	}
	same(t, "seats", e.findIDs(nil), d)
	same(t, "car_class", e.findIDs(map[string]any{"seats": nil, "car_class": "comfort"}), d)

	// bloklangan -> takliflardan chiqadi (profil to'liq almashtiriladi)
	if r := e.call("POST", path, body(map[string]any{"status": "blocked"})); r.Status != 204 {
		t.Fatalf("blocked: %d %s", r.Status, r.Raw)
	}
	same(t, "blocked", e.findIDs(nil))

	for name, b := range map[string]any{
		"status yo'q":       map[string]any{"driverId": d.String(), "seats": 4},
		"status noto'g'ri":  body(map[string]any{"status": "available"}),
		"driverId boshqa":   body(map[string]any{"driverId": uuid.NewString()}),
		"driverId yo'q":     map[string]any{"status": "active"},
		"seats 9":           body(map[string]any{"seats": 9}),
		"carClass":          body(map[string]any{"carClass": "vip"}),
		"vehicleType":       body(map[string]any{"vehicleType": "bus"}),
		"location.lat":      body(map[string]any{"location": map[string]any{"lat": 91, "lng": 1}}),
		"snake_case maydon": body(map[string]any{"car_class": "comfort"}),
	} {
		if r := e.call("POST", path, b); r.Status != 422 {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	if r := e.call("POST", "/internal/matching/drivers/not-a-uuid/profile", body(nil)); r.Status != 422 {
		t.Errorf("noto'g'ri id: %d", r.Status)
	}
}

func TestDriverWithoutProfileOrLocationIsNotMatched(t *testing.T) {
	e := newEnv(t)
	noProfile := uuid.New()
	e.location(noProfile, north(1), baseLng)
	noLoc := uuid.New()
	e.profile(noLoc, map[string]any{"state": "active", "seats": 4})
	same(t, "profilsiz/joylashuvsiz", e.findIDs(nil))
	// TTL: 2 daqiqa harakatsiz
	d := e.driver(1, nil)
	same(t, "jonli", e.findIDs(nil), d)
	e.mr.FastForward(2*time.Minute + time.Second)
	same(t, "2 daqiqadan keyin", e.findIDs(nil))
}

func TestRoute(t *testing.T) {
	e := newEnv(t)
	// Pricing bilan bir xil formula va bir xil natija (services/pricing testlari: 4615 m, 554 s)
	r := e.call("POST", "/internal/matching/route", map[string]any{
		"from": map[string]any{"lat": 41.3111, "lng": 69.2797}, "to": map[string]any{"lat": 41.2995, "lng": 69.2401}})
	if r.Status != 200 || r.Body["distance_m"] != float64(4615) || r.Body["duration_s"] != float64(554) {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	same0 := map[string]any{"lat": 1, "lng": 1}
	if r := e.call("POST", "/internal/matching/route", map[string]any{"from": same0, "to": same0}); r.Body["distance_m"] != float64(0) {
		t.Errorf("bir xil nuqta: %s", r.Raw)
	}
	for name, body := range map[string]any{
		"from yo'q": map[string]any{"to": same0}, "to yo'q": map[string]any{"from": same0},
		"lat": map[string]any{"from": map[string]any{"lat": 91, "lng": 1}, "to": same0}, "ortiqcha": map[string]any{"from": same0, "to": same0, "mode": "walk"},
	} {
		if r := e.call("POST", "/internal/matching/route", body); r.Status != 422 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
}

// Ko'p haydovchi bir vaqtda joylashuv yuboradi (har 10 s): poyga yo'q.
func TestConcurrentLocationUpdates(t *testing.T) {
	e := newEnv(t)
	const n = 50
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, n)
	for i := range ids {
		ids[i] = uuid.New()
		e.profile(ids[i], map[string]any{"state": "active", "seats": 4})
	}
	for round := 0; round < 3; round++ {
		for i, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if r := e.location(id, north(float64(i)*0.1), baseLng); r.Status != 200 {
					t.Errorf("%d: %d", i, r.Status)
				}
			}()
		}
		wg.Wait()
	}
	if got := e.findIDs(map[string]any{"radius_m": 100_000, "limit": 100}); len(got) != n {
		t.Errorf("%d ta haydovchi topildi, kutilgan %d", len(got), n)
	}
}
