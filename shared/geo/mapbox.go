package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mapbox Directions API — https://docs.mapbox.com/api/navigation/directions/
// Token faqat muhitdan (MAPBOX_TOKEN) olinadi: kodga, logga va xato matniga yozilmaydi.

const (
	// MapboxBaseURL — real API manzili (testlarda httptest serveri bilan almashtiriladi).
	MapboxBaseURL = "https://api.mapbox.com"
	// MapboxTimeout — bitta so'rov uchun chegara; undan keyin chaqiruvchi Haversine'ga qaytadi (FallbackRouter).
	// Trip & Order ichki chaqiruvni EXTERNAL_TIMEOUT (default 3 s) kutadi: Mapbox osilib qolsa ham Pricing va
	// Matching&Geo shu muddat ichida Haversine natijasini qaytarishga ulgurishi kerak — shuning uchun 1.5 s.
	MapboxTimeout = 1500 * time.Millisecond
	// mapboxMaxBody — javob hajmi chegarasi (overview=false da javob ~1 KiB).
	mapboxMaxBody = 1 << 20
)

// MAPBOX_MODE qiymatlari. Bo'sh — avtomatik: token bo'lsa real, bo'lmasa mock.
const (
	ModeMock = "mock"
	ModeReal = "real"
)

// ErrNoRoute — Mapbox ishladi, lekin shu ikki nuqta orasida yo'l topmadi (masalan, nuqta yo'ldan uzoq).
// Bu Mapbox'ning nosozligi emas: FallbackRouter faqat shu so'rov uchun Haversine'ga qaytadi.
var ErrNoRoute = errors.New("mapbox: marshrut topilmadi")

// MapboxRouter — real yo'l masofasi va davomiyligi (driving profili).
type MapboxRouter struct {
	token   string
	baseURL string
	client  *http.Client
}

// NewMapboxRouter — baseURL bo'sh bo'lsa MapboxBaseURL, client nil bo'lsa MapboxTimeout bilan yangi klient.
func NewMapboxRouter(token, baseURL string, client *http.Client) (*MapboxRouter, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("geo: MAPBOX_TOKEN bo'sh")
	}
	if baseURL == "" {
		baseURL = MapboxBaseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("geo: Mapbox manzili noto'g'ri")
	}
	if client == nil {
		client = &http.Client{Timeout: MapboxTimeout}
	}
	return &MapboxRouter{token: token, baseURL: strings.TrimRight(baseURL, "/"), client: client}, nil
}

// coord — Mapbox 6 xonadan ortiq aniqlikni e'tiborga olmaydi (~0.1 m).
func coord(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

func (m *MapboxRouter) Route(ctx context.Context, from, to Point) (Route, error) {
	// Mapbox koordinatani "lng,lat" tartibida kutadi.
	u := m.baseURL + "/directions/v5/mapbox/driving/" +
		coord(from.Lng) + "," + coord(from.Lat) + ";" + coord(to.Lng) + "," + coord(to.Lat) +
		"?overview=false&access_token=" + url.QueryEscape(m.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Route{}, errors.New("mapbox: so'rovni tuzib bo'lmadi")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return Route{}, fmt.Errorf("mapbox: %w", m.scrub(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, mapboxMaxBody))
	if err != nil {
		return Route{}, fmt.Errorf("mapbox: javobni o'qib bo'lmadi: %w", m.scrub(err))
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Routes  []struct {
			Distance float64 `json:"distance"` // metr
			Duration float64 `json:"duration"` // soniya
		} `json:"routes"`
	}
	decodeErr := json.Unmarshal(raw, &body)
	if resp.StatusCode != http.StatusOK {
		// 422 NoRoute / NoSegment — nuqtalar muammosi; qolganlari (401, 403, 429, 5xx) — xizmat muammosi.
		if body.Code == "NoRoute" || body.Code == "NoSegment" {
			return Route{}, fmt.Errorf("%w (code=%s)", ErrNoRoute, body.Code)
		}
		return Route{}, fmt.Errorf("mapbox: status %d%s", resp.StatusCode, m.detail(body.Code, body.Message))
	}
	if decodeErr != nil {
		return Route{}, errors.New("mapbox: javob JSON emas")
	}
	if body.Code != "Ok" || len(body.Routes) == 0 {
		return Route{}, fmt.Errorf("%w (code=%s)", ErrNoRoute, m.redact(clip(body.Code)))
	}
	d, s := body.Routes[0].Distance, body.Routes[0].Duration
	if math.IsNaN(d) || math.IsNaN(s) || d < 0 || s < 0 || d > math.MaxInt32 || s > math.MaxInt32 {
		return Route{}, errors.New("mapbox: javobdagi masofa/davomiylik noto'g'ri")
	}
	return Route{DistanceM: int(math.Round(d)), DurationS: int(math.Round(s))}, nil
}

// scrub — *url.Error to'liq manzilni (access_token bilan) o'z ichiga oladi: faqat sababi qoldiriladi.
// errors.Is(err, context.DeadlineExceeded) kabi tekshiruvlar saqlanadi.
func (m *MapboxRouter) scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, redacted{err: ue.Err, token: m.token})
	}
	return redacted{err: err, token: m.token}
}

func (m *MapboxRouter) detail(code, message string) string {
	out := ""
	if code != "" {
		out += " code=" + m.redact(clip(code))
	}
	if message != "" {
		out += ": " + m.redact(clip(message))
	}
	return out
}

func (m *MapboxRouter) redact(s string) string { return strings.ReplaceAll(s, m.token, "***") }

func clip(s string) string {
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// redacted — xato matnida token uchrasa yashiradi (qo'shimcha himoya); Unwrap orqali asl xato tekshiriladi.
type redacted struct {
	err   error
	token string
}

func (r redacted) Error() string { return strings.ReplaceAll(r.err.Error(), r.token, "***") }
func (r redacted) Unwrap() error { return r.err }

// ---------------------------------------------------------------- fallback

// DefaultCooldown — Mapbox nosoz bo'lganda shuncha vaqt u chaqirilmaydi (har so'rov timeout kutmasligi uchun).
const DefaultCooldown = 30 * time.Second

// FallbackRouter — avval Primary (Mapbox); u javob bermasa Fallback (Haversine). Hech qachon Primary xatosini
// qaytarmaydi: narx va ETA Mapbox'siz ham hisoblanadi.
//
// Xizmat xatosidan (tarmoq, timeout, 401/429/5xx) keyin Cooldown davomida Primary chaqirilmaydi; ErrNoRoute va
// chaqiruvchi konteksti bekor bo'lishi tanaffusga sabab bo'lmaydi. OnFallback faqat Primary haqiqatan chaqirilib
// xato berganda ishlaydi (tanaffus paytida har so'rovga log yozilmaydi).
type FallbackRouter struct {
	primary    Router
	fallback   Router
	cooldown   time.Duration
	onFallback func(err error)
	now        func() time.Time

	mu        sync.Mutex
	downUntil time.Time
}

// NewFallbackRouter — cooldown <= 0 bo'lsa DefaultCooldown; onFallback nil bo'lishi mumkin.
func NewFallbackRouter(primary, fallback Router, cooldown time.Duration, onFallback func(err error)) *FallbackRouter {
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	return &FallbackRouter{primary: primary, fallback: fallback, cooldown: cooldown, onFallback: onFallback, now: time.Now}
}

func (f *FallbackRouter) Route(ctx context.Context, from, to Point) (Route, error) {
	f.mu.Lock()
	down := f.now().Before(f.downUntil)
	f.mu.Unlock()
	if down {
		return f.fallback.Route(ctx, from, to)
	}
	rt, err := f.primary.Route(ctx, from, to)
	if err == nil {
		return rt, nil
	}
	// Chaqiruvchining o'zi bekor qilgan/vaqti tugagan bo'lsa — bu Primary'ning aybi emas.
	if ctx.Err() == nil && !errors.Is(err, ErrNoRoute) {
		f.mu.Lock()
		f.downUntil = f.now().Add(f.cooldown)
		f.mu.Unlock()
	}
	if f.onFallback != nil {
		f.onFallback(err)
	}
	return f.fallback.Route(ctx, from, to)
}

// ---------------------------------------------------------------- tanlash

// Config — marshrut manbai sozlamalari (Pricing va Matching&Geo bir xil ishlatadi, shuning uchun masofa mos keladi).
type Config struct {
	RoadFactor  float64
	AvgSpeedKMH float64
	// MapboxMode: "real" | "mock" | "" (avtomatik: token bo'lsa real).
	MapboxMode  string
	MapboxToken string
	// OnFallback — Mapbox xato berib Haversine ishlatilganda (log uchun).
	OnFallback func(err error)
}

// ValidMode — MAPBOX_MODE qiymati to'g'rimi (config tekshiruvi uchun).
func ValidMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModeMock, ModeReal:
		return true
	}
	return false
}

// UseMapbox — Mapbox ishlatiladimi: rejim "mock" emas va token bor.
func UseMapbox(mode, token string) bool {
	return !strings.EqualFold(strings.TrimSpace(mode), ModeMock) && strings.TrimSpace(token) != ""
}

// NewRouter — sozlamaga ko'ra Router: Mapbox (Haversine zaxirasi bilan) yoki faqat Haversine.
// Ikkinchi qiymat — Mapbox yoqilganmi (ishga tushishda logga yozish uchun).
func NewRouter(c Config) (Router, bool, error) {
	hv, err := NewHaversineRouter(c.RoadFactor, c.AvgSpeedKMH)
	if err != nil {
		return nil, false, err
	}
	if !UseMapbox(c.MapboxMode, c.MapboxToken) {
		return hv, false, nil
	}
	mb, err := NewMapboxRouter(c.MapboxToken, "", nil)
	if err != nil {
		return nil, false, err
	}
	return NewFallbackRouter(mb, hv, 0, c.OnFallback), true, nil
}
