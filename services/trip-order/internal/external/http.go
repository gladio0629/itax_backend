package external

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/domain"
)

// InternalTokenHeader — /internal/* chaqiruvlari uchun servislararo umumiy sir (INTERNAL_TOKENS).
const InternalTokenHeader = "X-Internal-Token"

// maxRespBytes — ichki javob tanasi uchun yuqori chegara (buzilgan/yolg'on javob xotirani to'ldirmasin).
const maxRespBytes = 1 << 20

// StatusError — ichki servis 2xx bo'lmagan javob qaytardi. Key — javobdagi error.key (bo'lsa).
type StatusError struct {
	Service string
	Status  int
	Key     string
}

func (e *StatusError) Error() string {
	if e.Key != "" {
		return fmt.Sprintf("%s: HTTP %d %s", e.Service, e.Status, e.Key)
	}
	return fmt.Sprintf("%s: HTTP %d", e.Service, e.Status)
}

// client — bitta ichki servisga JSON POST yuboruvchi umumiy yordamchi.
type client struct {
	service string
	baseURL string
	token   string
	hc      *http.Client
}

func newClient(service, baseURL, token string, hc *http.Client) client {
	return client{service: service, baseURL: strings.TrimRight(baseURL, "/"), token: token, hc: hc}
}

func (c client) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("%s: so'rovni kodlash: %w", c.service, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: so'rov: %w", c.service, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set(InternalTokenHeader, c.token)

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", c.service, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return fmt.Errorf("%s: javobni o'qish: %w", c.service, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var env struct {
			Error struct {
				Key string `json:"key"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env) // xato tanasi ixtiyoriy
		return &StatusError{Service: c.service, Status: resp.StatusCode, Key: env.Error.Key}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: javobni dekodlash: %w", c.service, err)
	}
	return nil
}

type pointJSON struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func toPoint(p domain.Point) pointJSON { return pointJSON{Lat: p.Lat, Lng: p.Lng} }

// ---------------------------------------------------------------- Pricing

// HTTPPricer — real Pricing servisi: POST {PRICING_URL}/internal/pricing/calculate.
type HTTPPricer struct{ c client }

func NewHTTPPricer(baseURL, token string, hc *http.Client) HTTPPricer {
	return HTTPPricer{c: newClient("pricing", baseURL, token, hc)}
}

// pricingCalculateReq — services/pricing/openapi.yml CalculateRequest (additionalProperties: false,
// shuning uchun scheduled_at yuborilmaydi).
type pricingCalculateReq struct {
	Type        domain.OrderType    `json:"type"`
	Seats       *int                `json:"seats,omitempty"`
	CarClass    *domain.CarClass    `json:"car_class,omitempty"`
	VehicleType *domain.VehicleType `json:"vehicle_type,omitempty"`
	From        pointJSON           `json:"from"`
	To          pointJSON           `json:"to"`
}

type pricingCalculateResp struct {
	Price      int64 `json:"price"`
	Commission int64 `json:"commission"`
	DistanceM  int   `json:"distance_m"`
	DurationS  int   `json:"duration_s"`
}

func (p HTTPPricer) Quote(ctx context.Context, r QuoteRequest) (Quote, error) {
	var out pricingCalculateResp
	err := p.c.post(ctx, "/internal/pricing/calculate", pricingCalculateReq{
		Type: r.Type, Seats: r.Seats, CarClass: r.CarClass, VehicleType: r.VehicleType, From: toPoint(r.From), To: toPoint(r.To),
	}, &out)
	if err != nil {
		return Quote{}, err
	}
	// Narx faqat shu javobdan olinadi — ma'nosiz qiymat buyurtmaga yozilmasin.
	if out.Price <= 0 || out.Commission < 0 || out.Commission > out.Price || out.DistanceM < 0 || out.DurationS < 0 {
		return Quote{}, fmt.Errorf("pricing: noto'g'ri javob (price=%d commission=%d distance_m=%d duration_s=%d)",
			out.Price, out.Commission, out.DistanceM, out.DurationS)
	}
	return Quote{Price: out.Price, Commission: out.Commission, DistanceM: out.DistanceM, DurationS: out.DurationS}, nil
}

// Finalize — Pricing'da yakuniy narx endpointi hali yo'q: kelishilgan narx o'zgarmaydi (mock bilan bir xil).
func (HTTPPricer) Finalize(_ context.Context, r FinalizeRequest) (Final, error) {
	return Final{Price: r.Quoted.Price, Commission: r.Quoted.Commission, DistanceM: r.Quoted.DistanceM}, nil
}

// ---------------------------------------------------------------- Matching

// matchingLimit — mock bilan bir xil: bitta buyurtmaga eng ko'pi 20 ta taklif.
const matchingLimit = 20

// HTTPMatcher — real Matching & Geo servisi: POST {MATCHING_URL}/internal/matching/find.
//
// order_id YUBORILMAYDI: aks holda matching-geo o'z takliflarini (boshqa offer_id bilan) yaratib offer.created
// chiqaradi, trip-order esa takliflarni o'zi saqlaydi — qabul qilishda ID lar mos kelmaydi.
type HTTPMatcher struct{ c client }

func NewHTTPMatcher(baseURL, token string, hc *http.Client) HTTPMatcher {
	return HTTPMatcher{c: newClient("matching", baseURL, token, hc)}
}

// matchingFindReq — services/matching-geo/openapi.yml FindRequest (server DisallowUnknownFields).
type matchingFindReq struct {
	Type        domain.OrderType    `json:"type"`
	Seats       *int                `json:"seats,omitempty"`
	CarClass    *domain.CarClass    `json:"car_class,omitempty"`
	VehicleType *domain.VehicleType `json:"vehicle_type,omitempty"`
	From        pointJSON           `json:"from"`
	Limit       int                 `json:"limit"`
}

type matchingFindResp struct {
	Drivers []struct {
		DriverID   uuid.UUID `json:"driver_id"`
		PickupETAs int       `json:"pickup_eta_s"`
	} `json:"drivers"`
}

func (m HTTPMatcher) FindDrivers(ctx context.Context, o domain.Order) ([]Candidate, error) {
	var out matchingFindResp
	err := m.c.post(ctx, "/internal/matching/find", matchingFindReq{
		Type: o.Type, Seats: o.Seats, CarClass: o.CarClass, VehicleType: o.VehicleType, From: toPoint(o.From), Limit: matchingLimit,
	}, &out)
	if err != nil {
		return nil, err
	}
	cands := make([]Candidate, 0, len(out.Drivers))
	seen := make(map[uuid.UUID]struct{}, len(out.Drivers))
	for _, d := range out.Drivers {
		if d.DriverID == uuid.Nil {
			continue
		}
		if _, dup := seen[d.DriverID]; dup {
			continue // bitta haydovchiga ikki taklif bo'lmasin
		}
		seen[d.DriverID] = struct{}{}
		cands = append(cands, Candidate{DriverID: d.DriverID, PickupETASeconds: max(d.PickupETAs, 0)})
		if len(cands) == matchingLimit {
			break
		}
	}
	return cands, nil
}

// ---------------------------------------------------------------- Wallet

// HTTPWallet — real Wallet servisi (F-13): POST {WALLET_URL}/internal/wallet/check-limit.
type HTTPWallet struct{ c client }

func NewHTTPWallet(baseURL, token string, hc *http.Client) HTTPWallet {
	return HTTPWallet{c: newClient("wallet", baseURL, token, hc)}
}

type walletCheckReq struct {
	DriverID uuid.UUID `json:"driver_id"`
}

// walletCheckResp — wallet CheckLimitResponse (Jackson SNAKE_CASE). allowed majburiy: yo'q bo'lsa xato.
type walletCheckResp struct {
	Allowed *bool `json:"allowed"`
	Limit   int64 `json:"limit"`
}

func (w HTTPWallet) CheckDriver(ctx context.Context, driverID uuid.UUID) (WalletStatus, error) {
	var out walletCheckResp
	if err := w.c.post(ctx, "/internal/wallet/check-limit", walletCheckReq{DriverID: driverID}, &out); err != nil {
		return WalletStatus{}, err
	}
	if out.Allowed == nil {
		return WalletStatus{}, errors.New("wallet: javobda allowed yo'q")
	}
	return WalletStatus{LimitReached: !*out.Allowed, Limit: out.Limit}, nil
}

// ---------------------------------------------------------------- tanlash

// Endpoints — real servislar manzillari. URL bo'sh bo'lsa shu servis uchun mock ishlaydi.
type Endpoints struct {
	PricingURL  string
	MatchingURL string
	WalletURL   string
	Token       string // X-Internal-Token
	Timeout     time.Duration
}

// Clients — servisga beriladigan implementatsiyalar va qaysi biri real ekanligi (log uchun).
type Clients struct {
	Pricer  Pricer
	Wallet  Wallet
	Matcher Matcher
	Real    map[string]bool
}

// New — har bir servis uchun alohida: URL berilgan bo'lsa real HTTP, aks holda mock.
// mockMatcher — matching mock'i (Redis'ga bog'liq, shuning uchun tashqaridan beriladi).
func New(e Endpoints, mockMatcher Matcher) Clients {
	hc := &http.Client{Timeout: e.Timeout}
	c := Clients{Pricer: MockPricer{}, Wallet: AllowAllWallet{}, Matcher: mockMatcher, Real: map[string]bool{}}
	if u := strings.TrimSpace(e.PricingURL); u != "" {
		c.Pricer, c.Real["pricing"] = NewHTTPPricer(u, e.Token, hc), true
	}
	if u := strings.TrimSpace(e.MatchingURL); u != "" {
		c.Matcher, c.Real["matching"] = NewHTTPMatcher(u, e.Token, hc), true
	}
	if u := strings.TrimSpace(e.WalletURL); u != "" {
		c.Wallet, c.Real["wallet"] = NewHTTPWallet(u, e.Token, hc), true
	}
	return c
}
