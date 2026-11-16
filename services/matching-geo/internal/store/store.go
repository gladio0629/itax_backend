package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	geoKey        = "drivers:geo"
	anomalyStream = "matching:gps-anomalies"
	// available/offline kalitlari ham abadiy sizib qolmasin: faol haydovchida har yozishda yangilanadi.
	idleStatusTTL = 7 * 24 * time.Hour
	tripDoneTTL   = 48 * time.Hour
)

type Options struct {
	LocationTTL time.Duration
	BusyTTL     time.Duration
	// StationaryAfter (F-11) — GPS nuqtasi aynan bir xil qolsa, shuncha vaqtdan keyin haydovchi avtomatik offline
	// (qidiruvda ko'rinmaydi). 0 — o'chirilgan.
	StationaryAfter time.Duration
}

type Store struct {
	rdb redis.Cmdable
	opt Options
}

func New(rdb redis.Cmdable, opt Options) *Store { return &Store{rdb: rdb, opt: opt} }

// ---------------------------------------------------------------- joylashuv

type Location struct {
	Lat, Lng float64
	At       time.Time // oxirgi yangilanish
	Since    time.Time // shu nuqta qachondan beri o'zgarmagan (birinchi marta ko'rilgan payt)
}

// Stale (F-11, o'zgarmas GPS) holatining o'zgarishi — log faqat o'tish paytida yoziladi.
type StaleChange int

const (
	StaleNone      StaleChange = iota // o'zgarish yo'q
	StaleEntered                      // hozirgina offline deb belgilandi
	StaleRecovered                    // nuqta o'zgardi: marker olib tashlandi
)

// Bitta atomik amal: oldingi nuqtani o'qish + GEOADD + jonlilik kaliti (TTL) + o'zgarmas-nuqta markeri.
// Oldingi nuqta tezlik tekshiruvi uchun. "Bir xil nuqta" — koordinata satrlari aynan teng (fmtFloat, epsilon yo'q):
// haqiqiy GPS turgan joyida ham tebranadi, aynan bir xil qiymat — qotib qolgan yoki soxta GPS alomati.
// Band (busy) haydovchi belgilanmaydi (safar davomida tirbandlik/kutish bo'lishi mumkin).
//
// KEYS: geo, loc, status, stale. ARGV: id, lng, lat, now_ms, ttl_s, stationary_ms (0 — o'chiq).
// Natija: {oldingi loc yoki ”, o'tish (0/1/2), since_ms}.
var updateScript = redis.NewScript(`
local prev = redis.call('GET', KEYS[2])
local since = ARGV[4]
local same = false
if prev then
  local plat, plng, pat, psince = string.match(prev, '^([^,]+),([^,]+),([^,]+),?([^,]*)$')
  if plat == ARGV[3] and plng == ARGV[2] then
    same = true
    if psince and psince ~= '' then since = psince else since = pat end
  end
end
redis.call('GEOADD', KEYS[1], ARGV[2], ARGV[3], ARGV[1])
redis.call('SET', KEYS[2], ARGV[3] .. ',' .. ARGV[2] .. ',' .. ARGV[4] .. ',' .. since, 'EX', ARGV[5])
local change = 0
local limit = tonumber(ARGV[6])
local busy = redis.call('GET', KEYS[3]) == 'busy'
if same and limit > 0 and (not busy) and (tonumber(ARGV[4]) - tonumber(since)) >= limit then
  local had = redis.call('EXISTS', KEYS[4])
  redis.call('SET', KEYS[4], since, 'EX', ARGV[5])
  if had == 0 then change = 1 end
elseif redis.call('DEL', KEYS[4]) == 1 then
  change = 2
end
return {prev or '', change, since}
`)

// UpdateLocation — GEOADD + TTL. Oldingi (hali o'chmagan) nuqtani (bo'lmasa nil) va o'zgarmas-nuqta holati o'zgarishini
// qaytaradi. Stale marker LocationTTL bilan yashaydi: haydovchi yuborishni to'xtatsa, u ham o'chadi.
func (s *Store) UpdateLocation(ctx context.Context, id uuid.UUID, lat, lng float64, now time.Time) (*Location, StaleChange, error) {
	res, err := updateScript.Run(ctx, s.rdb,
		[]string{geoKey, driverKey(id, "loc"), driverKey(id, "status"), driverKey(id, "stale")},
		id.String(), fmtFloat(lng), fmtFloat(lat), now.UnixMilli(), int(s.opt.LocationTTL.Seconds()),
		s.opt.StationaryAfter.Milliseconds()).Slice()
	if err != nil {
		return nil, StaleNone, fmt.Errorf("store: joylashuv: %w", err)
	}
	if len(res) != 3 {
		return nil, StaleNone, fmt.Errorf("store: joylashuv: kutilmagan javob: %v", res)
	}
	prev, _ := res[0].(string)
	change, _ := res[1].(int64)
	return parseLoc(prev), StaleChange(change), nil
}

// IsStale — haydovchi o'zgarmas GPS sababli avtomatik offline deb belgilanganmi (F-11).
func (s *Store) IsStale(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := s.rdb.Exists(ctx, driverKey(id, "stale")).Result()
	if err != nil {
		return false, fmt.Errorf("store: stale: %w", err)
	}
	return n > 0, nil
}

func fmtFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// parseLoc — "lat,lng,at_ms[,since_ms]" (eski 3 maydonli yozuvlar ham o'qiladi: since = at).
func parseLoc(s string) *Location {
	parts := strings.Split(s, ",")
	if len(parts) != 3 && len(parts) != 4 {
		return nil
	}
	lat, e1 := strconv.ParseFloat(parts[0], 64)
	lng, e2 := strconv.ParseFloat(parts[1], 64)
	ms, e3 := strconv.ParseInt(parts[2], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return nil
	}
	since := ms
	if len(parts) == 4 {
		v, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			return nil
		}
		since = v
	}
	return &Location{Lat: lat, Lng: lng, At: time.UnixMilli(ms), Since: time.UnixMilli(since)}
}

// Anomaly kind'lari (stream'dagi "kind").
const (
	AnomalySpeedJump  = "speed_jump"
	AnomalyStationary = "stationary"
)

// Anomaly — F-11 signali. Kind bo'sh bo'lsa speed_jump.
type Anomaly struct {
	DriverID  uuid.UUID
	Kind      string
	SpeedKMH  float64
	DistanceM float64
	Elapsed   time.Duration
	At        time.Time
}

// RecordAnomaly — signalni cheklangan Redis stream'iga yozadi (so'nggi ~1000 ta).
func (s *Store) RecordAnomaly(ctx context.Context, a Anomaly) error {
	kind := a.Kind
	if kind == "" {
		kind = AnomalySpeedJump
	}
	err := s.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: anomalyStream, MaxLen: 1000, Approx: true,
		Values: map[string]any{
			"driver_id": a.DriverID.String(), "kind": kind, "speed_kmh": fmtFloat(a.SpeedKMH),
			"distance_m": fmtFloat(a.DistanceM), "elapsed_ms": a.Elapsed.Milliseconds(), "at": a.At.UTC().Format(time.RFC3339Nano),
		},
	}).Err()
	if err != nil {
		return fmt.Errorf("store: anomaliya: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------- profil va holat

func (s *Store) SetProfile(ctx context.Context, id uuid.UUID, p Profile) error {
	pipe := s.rdb.TxPipeline()
	key := driverKey(id, "profile")
	pipe.Del(ctx, key)
	pipe.HSet(ctx, key, "state", p.State, "seats", p.Seats, "car_class", p.CarClass, "vehicle_type", p.VehicleType)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("store: profil: %w", err)
	}
	return nil
}

func parseProfile(m map[string]string) (Profile, bool) {
	if len(m) == 0 {
		return Profile{}, false
	}
	seats, _ := strconv.Atoi(m["seats"])
	return Profile{State: m["state"], Seats: seats, CarClass: m["car_class"], VehicleType: m["vehicle_type"]}, true
}

func (s *Store) GetProfile(ctx context.Context, id uuid.UUID) (Profile, bool, error) {
	m, err := s.rdb.HGetAll(ctx, driverKey(id, "profile")).Result()
	if err != nil {
		return Profile{}, false, fmt.Errorf("store: profil: %w", err)
	}
	p, ok := parseProfile(m)
	return p, ok, nil
}

func statusOf(raw string) Status {
	if st := Status(raw); st.Valid() {
		return st
	}
	return Available // kalit yo'q = joylashuv yuborayotgan haydovchi onlayn
}

func (s *Store) GetStatus(ctx context.Context, id uuid.UUID) (Status, error) {
	v, err := s.rdb.Get(ctx, driverKey(id, "status")).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("store: status: %w", err)
	}
	return statusOf(v), nil
}

var ErrDriverBusy = errors.New("store: driver busy")

// Lua: busy haydovchining holatini qo'lda o'zgartirib bo'lmaydi (faqat safar hodisalari o'zgartiradi).
var setStatusScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == 'busy' then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[2])
return 1
`)

// SetStatus — haydovchi onlayn/oflayn bo'ladi (available | offline). Band haydovchida ErrDriverBusy.
func (s *Store) SetStatus(ctx context.Context, id uuid.UUID, st Status) error {
	if st != Available && st != Offline {
		return fmt.Errorf("store: qo'lda faqat available/offline o'rnatiladi: %q", st)
	}
	n, err := setStatusScript.Run(ctx, s.rdb, []string{driverKey(id, "status")}, string(st), int(idleStatusTTL.Seconds())).Int()
	if err != nil {
		return fmt.Errorf("store: status: %w", err)
	}
	if n == 0 {
		return ErrDriverBusy
	}
	return nil
}

func doneKey(trip uuid.UUID) string { return "matching:trip-done:" + trip.String() }

// order.accepted: available -> busy. Safar allaqachon tugagan bo'lsa (hodisalar tartibsiz/takror kelsa) hech narsa qilinmaydi.
var markBusyScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[3]) == 1 then return 0 end
redis.call('SET', KEYS[1], 'busy', 'EX', ARGV[2])
redis.call('SET', KEYS[2], ARGV[1], 'EX', ARGV[2])
return 1
`)

// MarkBusy — safar qabul qilindi. applied=false: bu safar allaqachon tugagan edi.
func (s *Store) MarkBusy(ctx context.Context, driver, trip uuid.UUID) (bool, error) {
	n, err := markBusyScript.Run(ctx, s.rdb,
		[]string{driverKey(driver, "status"), driverKey(driver, "trip"), doneKey(trip)},
		trip.String(), int(s.opt.BusyTTL.Seconds())).Int()
	if err != nil {
		return false, fmt.Errorf("store: busy: %w", err)
	}
	return n == 1, nil
}

// trip.completed: busy -> available. Haydovchi boshqa safarda bo'lsa (kech kelgan eski hodisa) holat o'zgarmaydi;
// "offline" holat ham saqlanadi.
var markDoneScript = redis.NewScript(`
redis.call('SET', KEYS[3], '1', 'EX', ARGV[2])
local cur = redis.call('GET', KEYS[2])
if cur and cur ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[2])
local st = redis.call('GET', KEYS[1])
if (not st) or st == 'busy' then
  redis.call('SET', KEYS[1], 'available', 'EX', ARGV[3])
end
return 1
`)

// MarkTripCompleted — safar tugadi. released=true: haydovchi shu safardan bo'shadi.
func (s *Store) MarkTripCompleted(ctx context.Context, driver, trip uuid.UUID) (bool, error) {
	n, err := markDoneScript.Run(ctx, s.rdb,
		[]string{driverKey(driver, "status"), driverKey(driver, "trip"), doneKey(trip)},
		trip.String(), int(tripDoneTTL.Seconds()), int(idleStatusTTL.Seconds())).Int()
	if err != nil {
		return false, fmt.Errorf("store: tugatish: %w", err)
	}
	return n == 1, nil
}

// ---------------------------------------------------------------- qidiruv

// Find — radiusdagi mos haydovchilar, masofa bo'yicha o'sish tartibida.
//
// GEOSEARCH (Redis >= 6.2; GEORADIUS eskirgan) nomzodlarni beradi, so'ng holat/profil/jonlilik bo'yicha filtr qilinadi.
// Jonli bo'lmagan (joylashuvi muddati o'tgan) a'zolar geo-to'plamdan lazy tozalanadi.
func (s *Store) Find(ctx context.Context, q Query) ([]Candidate, error) {
	geo, err := s.rdb.GeoSearchLocation(ctx, geoKey, &redis.GeoSearchLocationQuery{
		GeoSearchQuery: redis.GeoSearchQuery{
			Longitude: q.Lng, Latitude: q.Lat, Radius: q.RadiusM, RadiusUnit: "m", Sort: "ASC", Count: q.MaxCandidates,
		},
		WithCoord: true, WithDist: true,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("store: geosearch: %w", err)
	}
	if len(geo) == 0 {
		return nil, nil
	}

	type probe struct {
		id      uuid.UUID
		loc     *redis.StringCmd
		stale   *redis.IntCmd
		status  *redis.StringCmd
		profile *redis.MapStringStringCmd
		g       redis.GeoLocation
	}
	probes := make([]probe, 0, len(geo))
	pipe := s.rdb.Pipeline()
	for _, g := range geo {
		id, err := uuid.Parse(g.Name)
		if err != nil {
			continue
		}
		probes = append(probes, probe{
			id: id, g: g,
			loc:     pipe.Get(ctx, driverKey(id, "loc")),
			stale:   pipe.Exists(ctx, driverKey(id, "stale")),
			status:  pipe.Get(ctx, driverKey(id, "status")),
			profile: pipe.HGetAll(ctx, driverKey(id, "profile")),
		})
	}
	// Qisman "redis: nil" xatolari kutilgan (kalit yo'q); haqiqiy xatolarni ajratamiz.
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("store: filtr: %w", err)
	}

	var out []Candidate
	var stale []any
	for _, p := range probes {
		live := p.loc.Err() == nil
		if !live {
			stale = append(stale, p.g.Name)
			continue
		}
		if p.stale.Val() > 0 { // F-11: GPS o'zgarmagan — avtomatik offline (geo-to'plamdan o'chirilmaydi, nuqta jonli)
			continue
		}
		prof, ok := parseProfile(p.profile.Val())
		if !ok {
			continue
		}
		if !Eligible(q, prof, statusOf(p.status.Val()), live) {
			continue
		}
		out = append(out, Candidate{DriverID: p.id, Lat: p.g.Latitude, Lng: p.g.Longitude, DistanceM: p.g.Dist})
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	if len(stale) > 0 {
		_ = s.rdb.ZRem(ctx, geoKey, stale...).Err() // best-effort
	}
	return out, nil
}

// Sweep — joylashuvi muddati o'tgan a'zolarni geo-to'plamdan olib tashlaydi (ZSCAN, bo'laklab).
func (s *Store) Sweep(ctx context.Context) (int, error) {
	var cursor uint64
	removed := 0
	for {
		members, next, err := s.rdb.ZScan(ctx, geoKey, cursor, "", 500).Result()
		if err != nil {
			return removed, fmt.Errorf("store: sweep: %w", err)
		}
		// ZSCAN a'zo, ball, a'zo, ball... qaytaradi
		var ids []string
		for i := 0; i < len(members); i += 2 {
			ids = append(ids, members[i])
		}
		if len(ids) > 0 {
			pipe := s.rdb.Pipeline()
			cmds := make([]*redis.IntCmd, len(ids))
			for i, id := range ids {
				cmds[i] = pipe.Exists(ctx, "driver:"+id+":loc")
			}
			if _, err := pipe.Exec(ctx); err != nil {
				return removed, fmt.Errorf("store: sweep: %w", err)
			}
			var dead []any
			for i, c := range cmds {
				if c.Val() == 0 {
					dead = append(dead, ids[i])
				}
			}
			if len(dead) > 0 {
				n, err := s.rdb.ZRem(ctx, geoKey, dead...).Result()
				if err != nil {
					return removed, fmt.Errorf("store: sweep: %w", err)
				}
				removed += int(n)
			}
		}
		if next == 0 {
			return removed, nil
		}
		cursor = next
	}
}
