package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/izgo/shared/geo"
	"github.com/izgo/trip-order/internal/apperr"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/izgo/trip-order/internal/external"
)

// ---------------------------------------------------------------- vaqt (aniq yoki oraliq)

// checkTiming — scheduled_at YOKI scheduled_from..scheduled_to (ikkalasi ham bo'lmasa — hozir).
func (s *Service) checkTiming(now time.Time, at, from, to *time.Time) error {
	if at != nil && (from != nil || to != nil) {
		return apperr.Validation(map[string]string{"scheduled_at": "conflicts_with_range"})
	}
	if at != nil {
		return s.checkSchedule(now, at)
	}
	if from == nil && to == nil {
		return nil
	}
	f := map[string]string{}
	switch {
	case from == nil:
		f["scheduled_from"] = "required"
	case to == nil:
		f["scheduled_to"] = "required"
	case !from.After(now):
		f["scheduled_from"] = "in_past"
	case !to.After(*from):
		f["scheduled_to"] = "before_from"
	case to.After(now.Add(s.opt.MaxScheduleAhead)):
		f["scheduled_to"] = "too_far"
	case to.Sub(*from) < s.opt.MinScheduleRange:
		f["scheduled_to"] = "range_too_short"
	case to.Sub(*from) > s.opt.MaxScheduleRange:
		f["scheduled_to"] = "range_too_long"
	}
	if len(f) > 0 {
		return apperr.Validation(f)
	}
	return nil
}

// scheduleBase — qidiruv muddati shu vaqtdan hisoblanadi: aniq vaqt yoki oraliq oxiri.
func scheduleBase(o domain.Order) *time.Time {
	if o.ScheduledTo != nil {
		return o.ScheduledTo
	}
	return o.ScheduledAt
}

// ---------------------------------------------------------------- narx

// priceKey — narxga ta'sir qiladigan maydonlar xeshi (quote va buyurtma bir xil bo'lishi shart).
// Vaqt, to'lov usuli, izoh va manzil matni narxga ta'sir qilmaydi — ular xeshga kirmaydi.
func priceKey(o domain.Order) string {
	var b strings.Builder
	fmt.Fprintf(&b, "v1|%s|", o.Type)
	if o.Seats != nil {
		fmt.Fprintf(&b, "seats=%d|", *o.Seats)
	}
	if len(o.SeatIDs) > 0 {
		ids := make([]string, len(o.SeatIDs))
		for i, v := range o.SeatIDs {
			ids[i] = string(v)
		}
		sort.Strings(ids)
		fmt.Fprintf(&b, "seat_ids=%s|", strings.Join(ids, ","))
	}
	if o.CarClass != nil {
		fmt.Fprintf(&b, "class=%s|", *o.CarClass)
	}
	if o.VehicleType != nil {
		fmt.Fprintf(&b, "vehicle=%s|", *o.VehicleType)
	}
	fmt.Fprintf(&b, "from=%.6f,%.6f|to=%.6f,%.6f", o.From.Lat, o.From.Lng, o.To.Lat, o.To.Lng)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// price — Pricing'dan narx + tarkib (tarkib bo'lmasa bitta qator).
func (s *Service) price(ctx context.Context, o domain.Order) (external.Quote, error) {
	q, err := s.pricer.Quote(ctx, external.QuoteRequest{
		Type: o.Type, Seats: o.Seats, CarClass: o.CarClass, VehicleType: o.VehicleType, From: o.From, To: o.To, ScheduledAt: scheduleBase(o),
	})
	if err != nil {
		return external.Quote{}, fmt.Errorf("pricing: %w", err)
	}
	if len(q.Lines) == 0 || domain.SumLines(q.Lines) != q.Price {
		q.Lines = domain.DefaultLines(o.Type, o.Seats, q.Price)
	}
	return q, nil
}

// QuoteOrder — POST /orders/quote: buyurtma bilan bir xil kirish; narx saqlanadi va quote_id bilan qaytadi.
// Buyurtma shu quote_id bilan yaratilsa, aynan shu narx yoziladi.
func (s *Service) QuoteOrder(ctx context.Context, passenger uuid.UUID, in CreateInput) (domain.Quote, error) {
	now := s.now()
	o := in.order(passenger)
	if err := s.validateNew(now, o); err != nil {
		return domain.Quote{}, err
	}
	p, err := s.price(ctx, o)
	if err != nil {
		return domain.Quote{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return domain.Quote{}, fmt.Errorf("uuid: %w", err)
	}
	q := domain.Quote{
		ID: id, PassengerID: passenger, InputHash: priceKey(o), Price: p.Price, Commission: p.Commission,
		DistanceM: p.DistanceM, DurationS: p.DurationS, Lines: p.Lines, CreatedAt: now, ExpiresAt: now.Add(s.opt.QuoteTTL),
	}
	if err := s.st.InsertQuote(ctx, q); err != nil {
		return domain.Quote{}, err
	}
	return q, nil
}

// quoted — quote_id bo'yicha narx: egasi, muddati, ishlatilmaganligi va maydonlar mosligi tekshiriladi.
// Narx hech qachon jimgina qayta hisoblanmaydi — mos kelmasa aniq xato.
func (s *Service) quoted(ctx context.Context, passenger uuid.UUID, o domain.Order, now time.Time) (domain.Quote, error) {
	id := o.QuoteID.String()
	q, err := s.st.GetQuote(ctx, *o.QuoteID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && q.PassengerID != passenger) {
		return domain.Quote{}, apperr.QuoteNotFound(id)
	}
	if err != nil {
		return domain.Quote{}, err
	}
	switch {
	case q.UsedOrderID != nil:
		return domain.Quote{}, apperr.QuoteAlreadyUsed(id)
	case !q.ExpiresAt.After(now):
		return domain.Quote{}, apperr.QuoteExpired(id)
	case q.InputHash != priceKey(o):
		return domain.Quote{}, apperr.QuoteMismatch(id)
	}
	return q, nil
}

// validateNew — yangi buyurtma/quote uchun umumiy tekshiruvlar.
func (s *Service) validateNew(now time.Time, o domain.Order) error {
	if err := o.CheckShape(); err != nil {
		return apperr.Validation(map[string]string{"type": "invalid_combination"})
	}
	if sameLocation(o.From, o.To) {
		return apperr.Validation(map[string]string{"to": "same_as_from"})
	}
	return s.checkTiming(now, o.ScheduledAt, o.ScheduledFrom, o.ScheduledTo)
}

// ---------------------------------------------------------------- shaharlar katalogi (kesh)

const catalogTTL = 5 * time.Minute

// nearCityRadiusM — nuqta shu radius ichida bo'lsa shu shaharniki hisoblanadi.
const nearCityRadiusM = 50_000

type catalogCache struct {
	mu     sync.Mutex
	cities []domain.City
	at     time.Time
	routes map[string]routePrice
}

type routePrice struct {
	price, distance int64
	at              time.Time
}

func (s *Service) Cities(ctx context.Context) ([]domain.City, error) {
	s.cat.mu.Lock()
	defer s.cat.mu.Unlock()
	if s.cat.cities != nil && time.Since(s.cat.at) < catalogTTL {
		return s.cat.cities, nil
	}
	cs, err := s.st.ListCities(ctx)
	if err != nil {
		return nil, err
	}
	if cs == nil {
		cs = []domain.City{}
	}
	s.cat.cities, s.cat.at = cs, time.Now()
	return cs, nil
}

func distM(aLat, aLng, bLat, bLng float64) int {
	return int(geo.Haversine(geo.Point{Lat: aLat, Lng: aLng}, geo.Point{Lat: bLat, Lng: bLng}) + 0.5)
}

// nearestCity — nuqtaga eng yaqin shahar va masofa (shahar bo'lmasa ok=false).
func nearestCity(cs []domain.City, lat, lng float64) (domain.City, int, bool) {
	best, bestD := domain.City{}, -1
	for _, c := range cs {
		if d := distM(lat, lng, c.Lat, c.Lng); bestD < 0 || d < bestD {
			best, bestD = c, d
		}
	}
	return best, bestD, bestD >= 0
}

// cityOf — nuqta shaharga yaqin (nearCityRadiusM) bo'lsa shu shahar ID si. Xato buyurtmani to'xtatmaydi.
func (s *Service) cityOf(ctx context.Context, p domain.Point) *uuid.UUID {
	cs, err := s.Cities(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "shaharlar o'qilmadi", "err", err)
		return nil
	}
	c, d, ok := nearestCity(cs, p.Lat, p.Lng)
	if !ok || d > nearCityRadiusM {
		return nil
	}
	id := c.ID
	return &id
}

// ---------------------------------------------------------------- manzil qidiruvi (server tomonida)

// NormalizeKey — qidiruv kaliti: kichik harf, apostroflarsiz, bo'shliqlar bitta (migratsiyadagi search_key bilan bir xil qoida).
func NormalizeKey(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r == '\'' || r == '`' || r == '‘' || r == '’' || r == 'ʻ' || r == 'ʼ':
			continue
		case unicode.IsSpace(r) || r == ',' || r == '-':
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

type SearchInput struct {
	Q      string
	Near   *domain.Point
	CityID *uuid.UUID
	Limit  int
}

// SearchPlaces — shaharlar + gazetteer manzillari. Tashqi geocoder kaliti klientga hech qachon chiqmaydi.
func (s *Service) SearchPlaces(ctx context.Context, in SearchInput) ([]domain.Place, error) {
	key := NormalizeKey(in.Q)
	if len([]rune(key)) < 2 {
		return nil, apperr.Validation(map[string]string{"q": "too_short"})
	}
	cs, err := s.Cities(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.Place
	for _, c := range cs {
		if in.CityID != nil && c.ID != *in.CityID {
			continue
		}
		if strings.Contains(NormalizeKey(c.Name), key) {
			out = append(out, domain.Place{ID: c.ID, CityID: c.ID, Name: c.Name, Area: c.Region, Kind: "city", Lat: c.Lat, Lng: c.Lng})
		}
	}
	ps, err := s.st.SearchPlaces(ctx, key, in.CityID, in.Limit)
	if err != nil {
		return nil, err
	}
	out = append(out, ps...)
	prefix := func(p domain.Place) bool { return strings.HasPrefix(NormalizeKey(p.Name), key) }
	if in.Near != nil {
		for i := range out {
			d := distM(in.Near.Lat, in.Near.Lng, out[i].Lat, out[i].Lng)
			out[i].DistanceM = &d
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if pi, pj := prefix(out[i]), prefix(out[j]); pi != pj {
			return pi
		}
		if in.Near != nil {
			return *out[i].DistanceM < *out[j].DistanceM
		}
		return false
	})
	if len(out) > in.Limit {
		out = out[:in.Limit]
	}
	return out, nil
}

// reverseRadiusM — teskari geocoding: shu radius ichidagi gazetteer manzili, bo'lmasa eng yaqin shahar.
const reverseRadiusM = 2_000

func (s *Service) ReversePlace(ctx context.Context, p domain.Point) (domain.Place, error) {
	ps, err := s.st.PlacesNear(ctx, p.Lat, p.Lng, 0.03)
	if err != nil {
		return domain.Place{}, err
	}
	best, bestD := domain.Place{}, -1
	for _, c := range ps {
		if d := distM(p.Lat, p.Lng, c.Lat, c.Lng); d <= reverseRadiusM && (bestD < 0 || d < bestD) {
			best, bestD = c, d
		}
	}
	if bestD < 0 {
		cs, err := s.Cities(ctx)
		if err != nil {
			return domain.Place{}, err
		}
		c, d, ok := nearestCity(cs, p.Lat, p.Lng)
		if !ok {
			return domain.Place{}, apperr.NotFound()
		}
		best, bestD = domain.Place{ID: c.ID, CityID: c.ID, Name: c.Name, Area: c.Region, Kind: "city", Lat: c.Lat, Lng: c.Lng}, d
	}
	best.DistanceM = &bestD
	return best, nil
}

// ---------------------------------------------------------------- saqlangan manzillar

const SavedPlacesLimit = 20

func (s *Service) SavedPlaces(ctx context.Context, user uuid.UUID) ([]domain.SavedPlace, error) {
	return s.st.ListSavedPlaces(ctx, user)
}

func (s *Service) SavePlace(ctx context.Context, p domain.SavedPlace) (domain.SavedPlace, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return domain.SavedPlace{}, err
	}
	p.ID, p.CreatedAt = id, s.now()
	out, err := s.st.CreateSavedPlace(ctx, p, SavedPlacesLimit)
	if errors.Is(err, domain.ErrLimitReached) {
		return domain.SavedPlace{}, apperr.Forbidden("saved_places_limit", map[string]any{"limit": SavedPlacesLimit})
	}
	return out, err
}

func (s *Service) DeleteSavedPlace(ctx context.Context, user, id uuid.UUID) error {
	if err := s.st.DeleteSavedPlace(ctx, user, id); errors.Is(err, domain.ErrNotFound) {
		return apperr.NotFound()
	} else if err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------- mashhur yo'nalishlar, seat map

// RouteView — yo'nalish + 1 o'rindiq narxi (shahar markazlari orasida, Pricing hisoblagan; xato bo'lsa nil).
type RouteView struct {
	Route     domain.PopularRoute
	DistanceM *int
	PriceFrom *int64
}

func (s *Service) PopularRoutes(ctx context.Context) ([]RouteView, error) {
	rs, err := s.st.PopularRoutes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]RouteView, 0, len(rs))
	for _, r := range rs {
		v := RouteView{Route: r}
		if p, d, ok := s.routePrice(ctx, r); ok {
			v.PriceFrom, v.DistanceM = &p, &d
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) routePrice(ctx context.Context, r domain.PopularRoute) (int64, int, bool) {
	key := r.From.ID.String() + ":" + r.To.ID.String()
	s.cat.mu.Lock()
	if v, ok := s.cat.routes[key]; ok && time.Since(v.at) < catalogTTL {
		s.cat.mu.Unlock()
		return v.price, int(v.distance), true
	}
	s.cat.mu.Unlock()
	one := 1
	q, err := s.pricer.Quote(ctx, external.QuoteRequest{Type: domain.TypePassenger, Seats: &one,
		From: domain.Point{Lat: r.From.Lat, Lng: r.From.Lng}, To: domain.Point{Lat: r.To.Lat, Lng: r.To.Lng}})
	if err != nil {
		s.log.WarnContext(ctx, "yo'nalish narxi hisoblanmadi", "route_id", r.ID, "err", err)
		return 0, 0, false
	}
	s.cat.mu.Lock()
	if s.cat.routes == nil {
		s.cat.routes = map[string]routePrice{}
	}
	s.cat.routes[key] = routePrice{price: q.Price, distance: int64(q.DistanceM), at: time.Now()}
	s.cat.mu.Unlock()
	return q.Price, q.DistanceM, true
}

// SeatView — seat map qatori.
type SeatView struct {
	ID    domain.SeatID
	Taken bool
	Price int64
}

// SeatMap — yo'nalish bo'yicha o'rindiqlar. Narx o'rindiq SONIGA bog'liq, shuning uchun har joy narxi = 1 o'rindiq narxi.
// Bir mashinaga bir nechta yo'lovchini jamlash (pooling) hali yo'q: har buyurtma alohida mashina, shuning uchun taken doim false.
func (s *Service) SeatMap(ctx context.Context, from, to domain.Point) ([]SeatView, error) {
	if sameLocation(from, to) {
		return nil, apperr.Validation(map[string]string{"to": "same_as_from"})
	}
	one := 1
	q, err := s.price(ctx, domain.Order{Type: domain.TypePassenger, Seats: &one, From: from, To: to})
	if err != nil {
		return nil, err
	}
	out := make([]SeatView, 0, len(domain.AllSeats))
	for _, id := range domain.AllSeats {
		out = append(out, SeatView{ID: id, Price: q.Price})
	}
	return out, nil
}

// ---------------------------------------------------------------- yuk mashinalari

// Vehicle — yetkazib berish mashinasi tavsifi (o'lchamlar — yuk bo'limi, sm).
type Vehicle struct {
	Type        domain.VehicleType
	SizeLabel   string
	WidthCm     int
	DepthCm     int
	HeightCm    int
	MaxWeightKg int
}

// DeliveryVehicles — klient "S, M, L, XL, XXL, 3XL" tugmalari; vehicle_type enum'i o'zgarmaydi.
var DeliveryVehicles = []Vehicle{
	{domain.Spark, "S", 170, 100, 90, 300},
	{domain.Damas, "M", 220, 130, 130, 700},
	{domain.Labo, "L", 280, 160, 50, 1000},
	{domain.Isuzu, "XL", 420, 200, 200, 3000},
	{domain.Isuzu2, "XXL", 600, 240, 240, 5000},
	{domain.Fura, "3XL", 840, 260, 240, 8000},
}
