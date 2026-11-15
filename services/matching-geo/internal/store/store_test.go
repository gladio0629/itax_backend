package store_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/izgo/matching-geo/internal/store"
	"github.com/izgo/shared/geo"
	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

type env struct {
	t   *testing.T
	mr  *miniredis.Miniredis
	rdb *redis.Client
	st  *store.Store
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &env{t: t, mr: mr, rdb: rdb, now: time.Now(),
		st: store.New(rdb, store.Options{LocationTTL: 2 * time.Minute, BusyTTL: 24 * time.Hour})}
}

// Toshkent markazi
const (
	baseLat = 41.3111
	baseLng = 69.2797
)

// north km kilometr shimolda (taxminan: 1° kenglik = 111.195 km)
func north(km float64) (float64, float64) { return baseLat + km/111.195, baseLng }

// driver — joylashuv + faol profil bilan haydovchi.
func (e *env) driver(km float64, p store.Profile) uuid.UUID {
	e.t.Helper()
	id := uuid.New()
	lat, lng := north(km)
	if _, err := e.st.UpdateLocation(ctx, id, lat, lng, e.now); err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.SetProfile(ctx, id, p); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func active(seats int) store.Profile { return store.Profile{State: "active", Seats: seats} }

func (e *env) find(q store.Query) []store.Candidate {
	e.t.Helper()
	if q.Kind == "" {
		q.Kind = store.KindPassenger
	}
	if q.Seats == nil && q.CarClass == nil && q.VehicleType == nil && q.Kind == store.KindPassenger {
		one := 1
		q.Seats = &one
	}
	q.Lat, q.Lng = baseLat, baseLng
	if q.RadiusM == 0 {
		q.RadiusM = 50_000
	}
	if q.MaxCandidates == 0 {
		q.MaxCandidates = 500
	}
	got, err := e.st.Find(ctx, q)
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

func ids(c []store.Candidate) []uuid.UUID {
	out := make([]uuid.UUID, len(c))
	for i, x := range c {
		out[i] = x.DriverID
	}
	return out
}

func equalIDs(t *testing.T, name string, got []store.Candidate, want ...uuid.UUID) {
	t.Helper()
	g := ids(got)
	if len(g) != len(want) {
		t.Fatalf("%s: %d ta natija, kutilgan %d", name, len(g), len(want))
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatalf("%s: %d-o'rin %s, kutilgan %s", name, i, g[i], want[i])
		}
	}
}

func TestGeoAddAndRadius(t *testing.T) {
	e := newEnv(t)
	near := e.driver(1, active(4))
	mid := e.driver(10, active(4))
	far := e.driver(60, active(4)) // 50 km radiusdan tashqarida

	// GEOADD: koordinata saqlangan
	pos, err := e.rdb.GeoPos(ctx, "drivers:geo", near.String()).Result()
	if err != nil || pos[0] == nil {
		t.Fatalf("geopos: %v %v", pos, err)
	}
	wantLat, wantLng := north(1)
	if math.Abs(pos[0].Latitude-wantLat) > 1e-4 || math.Abs(pos[0].Longitude-wantLng) > 1e-4 {
		t.Errorf("saqlangan nuqta: %+v", pos[0])
	}

	got := e.find(store.Query{})
	equalIDs(t, "50 km", got, near, mid) // masofa o'sish tartibida, far yo'q

	// masofalar metrda va Haversine bilan mos (GEO ~0.5% xato beradi)
	for _, c := range got {
		want := geo.Haversine(geo.Point{Lat: baseLat, Lng: baseLng}, geo.Point{Lat: c.Lat, Lng: c.Lng})
		if math.Abs(c.DistanceM-want) > want*0.005+1 {
			t.Errorf("%s: masofa %.1f, kutilgan %.1f", c.DriverID, c.DistanceM, want)
		}
	}
	if d := got[0].DistanceM; d < 990 || d > 1010 {
		t.Errorf("1 km haydovchi masofasi %.1f", d)
	}

	equalIDs(t, "5 km", e.find(store.Query{RadiusM: 5_000}), near)
	equalIDs(t, "100 km", e.find(store.Query{RadiusM: 100_000}), near, mid, far)
	equalIDs(t, "radius 500 m", e.find(store.Query{RadiusM: 500}))
	equalIDs(t, "limit 1", e.find(store.Query{RadiusM: 100_000, Limit: 1}), near)
}

func TestLocationTTLExpiry(t *testing.T) {
	e := newEnv(t)
	d := e.driver(1, active(4))
	if ttl := e.mr.TTL("driver:" + d.String() + ":loc"); ttl != 2*time.Minute {
		t.Fatalf("TTL = %v, 2 daqiqa bo'lishi kerak", ttl)
	}
	// harakatsiz 2 daqiqa -> joylashuv kaliti o'chadi, haydovchi qidiruvda chiqmaydi
	e.mr.FastForward(2*time.Minute + time.Second)
	if got := e.find(store.Query{}); len(got) != 0 {
		t.Fatalf("muddati o'tgan haydovchi topildi: %+v", got)
	}
	// geo-to'plamdan lazy tozalangan
	if n, _ := e.rdb.ZCard(ctx, "drivers:geo").Result(); n != 0 {
		t.Errorf("geo-to'plamda %d ta qoldiq", n)
	}
	// yangi joylashuv — qaytadi
	lat, lng := north(1)
	if prev, err := e.st.UpdateLocation(ctx, d, lat, lng, e.now); err != nil || prev != nil {
		t.Errorf("muddati o'tgach oldingi nuqta bo'lmasligi kerak: %+v %v", prev, err)
	}
	if got := e.find(store.Query{}); len(got) != 1 {
		t.Errorf("qaytadan jonli: %d", len(got))
	}
	// yangilanib turgan haydovchi TTL ni uzaytiradi
	e.mr.FastForward(90 * time.Second)
	if _, err := e.st.UpdateLocation(ctx, d, lat, lng, e.now); err != nil {
		t.Fatal(err)
	}
	e.mr.FastForward(90 * time.Second)
	if got := e.find(store.Query{}); len(got) != 1 {
		t.Errorf("TTL yangilanmadi: %d", len(got))
	}
}

func TestSweepRemovesStaleMembers(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		e.driver(float64(i+1), active(4))
	}
	e.mr.FastForward(3 * time.Minute)
	live := e.driver(2, active(4))
	removed, err := e.st.Sweep(ctx)
	if err != nil || removed != 5 {
		t.Fatalf("sweep: %d %v", removed, err)
	}
	if n, _ := e.rdb.ZCard(ctx, "drivers:geo").Result(); n != 1 {
		t.Errorf("qolgan a'zolar %d", n)
	}
	equalIDs(t, "jonli", e.find(store.Query{}), live)
}

func TestUpdateLocationReturnsPrevious(t *testing.T) {
	e := newEnv(t)
	id := uuid.New()
	if prev, _ := e.st.UpdateLocation(ctx, id, 41.0, 69.0, e.now); prev != nil {
		t.Fatal("birinchi marta oldingi nuqta yo'q")
	}
	prev, err := e.st.UpdateLocation(ctx, id, 41.5, 69.5, e.now.Add(10*time.Second))
	if err != nil || prev == nil || prev.Lat != 41.0 || prev.Lng != 69.0 || prev.At.UnixMilli() != e.now.UnixMilli() {
		t.Fatalf("prev = %+v %v", prev, err)
	}
}

func TestFindFilters(t *testing.T) {
	e := newEnv(t)
	two := 2
	three := 3
	econ, biz, labo, fura := "economy", "business", "labo", "fura"

	ok4 := e.driver(1, store.Profile{State: "active", Seats: 4, CarClass: "economy"})
	ok2 := e.driver(2, store.Profile{State: "active", Seats: 2, CarClass: "business"})
	cargo := e.driver(3, store.Profile{State: "active", VehicleType: "labo"})
	_ = e.driver(4, store.Profile{State: "blocked", Seats: 4})
	_ = e.driver(5, store.Profile{State: "pending_review", Seats: 4})
	busy := e.driver(6, active(4))
	offline := e.driver(7, active(4))
	_ = e.st.SetProfile(ctx, uuid.New(), active(4)) // joylashuvsiz
	noProfile := uuid.New()
	lat, lng := north(8)
	_, _ = e.st.UpdateLocation(ctx, noProfile, lat, lng, e.now)

	if _, err := e.st.MarkBusy(ctx, busy, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetStatus(ctx, offline, store.Offline); err != nil {
		t.Fatal(err)
	}

	// o'rindiq: faqat sig'imi yetarli, active, band emas, onlayn
	equalIDs(t, "seats=1", e.find(store.Query{Seats: ptr(1)}), ok4, ok2)
	equalIDs(t, "seats=2", e.find(store.Query{Seats: &two}), ok4, ok2)
	equalIDs(t, "seats=3", e.find(store.Query{Seats: &three}), ok4)
	equalIDs(t, "seats=4", e.find(store.Query{Seats: ptr(4)}), ok4)
	// butun mashina turi
	equalIDs(t, "car_class=economy", e.find(store.Query{Seats: nil, CarClass: &econ}), ok4)
	equalIDs(t, "car_class=business", e.find(store.Query{Seats: nil, CarClass: &biz}), ok2)
	// delivery faqat mos mashina turi
	equalIDs(t, "delivery labo", e.find(store.Query{Kind: store.KindDelivery, VehicleType: &labo}), cargo)
	equalIDs(t, "delivery fura", e.find(store.Query{Kind: store.KindDelivery, VehicleType: &fura}))
	// yo'lovchi tashimaydigan (seats=0) yuk haydovchisi passenger buyurtmasiga chiqmaydi — yuqorida seats=1 da cargo yo'q
}

func ptr[T any](v T) *T { return &v }

func TestEligible(t *testing.T) {
	p := store.Profile{State: "active", Seats: 3, CarClass: "Comfort", VehicleType: "damas"}
	seats := func(n int) store.Query { return store.Query{Kind: store.KindPassenger, Seats: &n} }
	cc := func(c string) store.Query { return store.Query{Kind: store.KindPassenger, CarClass: &c} }
	veh := func(v string) store.Query { return store.Query{Kind: store.KindDelivery, VehicleType: &v} }
	cases := []struct {
		name string
		q    store.Query
		p    store.Profile
		st   store.Status
		live bool
		want bool
	}{
		{"seats yetarli", seats(3), p, store.Available, true, true},
		{"seats yetmaydi", seats(4), p, store.Available, true, false},
		{"car_class (registrsiz)", cc("comfort"), p, store.Available, true, true},
		{"car_class boshqa", cc("business"), p, store.Available, true, false},
		{"delivery mos", veh("damas"), p, store.Available, true, true},
		{"delivery mos emas", veh("labo"), p, store.Available, true, false},
		{"busy", seats(1), p, store.Busy, true, false},
		{"offline", seats(1), p, store.Offline, true, false},
		{"jonli emas", seats(1), p, store.Available, false, false},
		{"blocked", seats(1), store.Profile{State: "blocked", Seats: 4}, store.Available, true, false},
		{"seats=0 haydovchi", seats(1), store.Profile{State: "active"}, store.Available, true, false},
		{"mashina turi yo'q", veh("labo"), store.Profile{State: "active", Seats: 4}, store.Available, true, false},
		{"noma'lum tur", store.Query{Kind: "x"}, p, store.Available, true, false},
		{"passenger, talab yo'q", store.Query{Kind: store.KindPassenger}, p, store.Available, true, false},
	}
	for _, c := range cases {
		if got := store.Eligible(c.q, c.p, c.st, c.live); got != c.want {
			t.Errorf("%s: %v, kutilgan %v", c.name, got, c.want)
		}
	}
}

func TestStatusTransitions(t *testing.T) {
	e := newEnv(t)
	d := e.driver(1, active(4))
	t1, t2 := uuid.New(), uuid.New()
	status := func() store.Status {
		s, err := e.st.GetStatus(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	eligible := func() bool { return len(e.find(store.Query{})) == 1 }

	// kalit yo'q = available
	if status() != store.Available || !eligible() {
		t.Fatal("yangi haydovchi available bo'lishi kerak")
	}
	// order.accepted: available -> busy
	if ok, err := e.st.MarkBusy(ctx, d, t1); err != nil || !ok {
		t.Fatalf("MarkBusy: %v %v", ok, err)
	}
	if status() != store.Busy || eligible() {
		t.Fatal("busy haydovchi taklif olmasligi kerak")
	}
	if ttl := e.mr.TTL("driver:" + d.String() + ":status"); ttl != 24*time.Hour {
		t.Errorf("busy TTL = %v", ttl)
	}
	// takroriy order.accepted — zarar yo'q
	if ok, _ := e.st.MarkBusy(ctx, d, t1); !ok || status() != store.Busy {
		t.Error("takroriy accepted")
	}
	// band haydovchini qo'lda available qilib bo'lmaydi
	if err := e.st.SetStatus(ctx, d, store.Available); err != store.ErrDriverBusy {
		t.Errorf("busy -> available (qo'lda): %v", err)
	}
	if err := e.st.SetStatus(ctx, d, store.Busy); err == nil {
		t.Error("qo'lda busy o'rnatildi")
	}
	// trip.completed: busy -> available
	if ok, err := e.st.MarkTripCompleted(ctx, d, t1); err != nil || !ok {
		t.Fatalf("MarkTripCompleted: %v %v", ok, err)
	}
	if status() != store.Available || !eligible() {
		t.Fatal("safar tugagach available bo'lishi kerak")
	}
	// takroriy trip.completed — zarar yo'q
	if _, err := e.st.MarkTripCompleted(ctx, d, t1); err != nil || status() != store.Available {
		t.Error("takroriy completed")
	}
	// KECH kelgan (takroriy) order.accepted tugagan safar uchun haydovchini band qilmaydi
	if ok, _ := e.st.MarkBusy(ctx, d, t1); ok || status() != store.Available {
		t.Errorf("kech kelgan accepted band qildi (ok=%v, status=%s)", ok, status())
	}
	// tartibsiz: completed(t2) accepted(t2) dan oldin kelsa ham haydovchi band bo'lib qolmaydi
	if _, err := e.st.MarkTripCompleted(ctx, d, t2); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.st.MarkBusy(ctx, d, t2); ok || status() != store.Available {
		t.Errorf("tartibsiz hodisalar: ok=%v status=%s", ok, status())
	}
	// eski safarning kech completed'i yangi safardagi haydovchini bo'shatmaydi
	t3, t4 := uuid.New(), uuid.New()
	_, _ = e.st.MarkBusy(ctx, d, t3)
	if ok, _ := e.st.MarkTripCompleted(ctx, d, t4); ok || status() != store.Busy {
		t.Errorf("begona safar tugashi haydovchini bo'shatdi (ok=%v, status=%s)", ok, status())
	}
	if ok, _ := e.st.MarkTripCompleted(ctx, d, t3); !ok || status() != store.Available {
		t.Errorf("t3 tugashi: ok=%v status=%s", ok, status())
	}
	// oflayn: safar tugashi oflaynni "available" qilmaydi
	if err := e.st.SetStatus(ctx, d, store.Offline); err != nil {
		t.Fatal(err)
	}
	if eligible() {
		t.Error("offline haydovchi taklif oldi")
	}
	t5 := uuid.New()
	_, _ = e.st.MarkTripCompleted(ctx, d, t5)
	if status() != store.Offline {
		t.Errorf("completed offline'ni buzdi: %s", status())
	}
	if err := e.st.SetStatus(ctx, d, store.Available); err != nil || !eligible() {
		t.Errorf("offline -> available: %v", err)
	}
}

func TestRecordAnomaly(t *testing.T) {
	e := newEnv(t)
	id := uuid.New()
	if err := e.st.RecordAnomaly(ctx, store.Anomaly{DriverID: id, SpeedKMH: 1234.5, DistanceM: 5000, Elapsed: 10 * time.Second, At: e.now}); err != nil {
		t.Fatal(err)
	}
	msgs, err := e.rdb.XRange(ctx, "matching:gps-anomalies", "-", "+").Result()
	if err != nil || len(msgs) != 1 || msgs[0].Values["driver_id"] != id.String() || msgs[0].Values["kind"] != "speed_jump" || msgs[0].Values["speed_kmh"] != "1234.5" {
		t.Fatalf("%+v %v", msgs, err)
	}
}
