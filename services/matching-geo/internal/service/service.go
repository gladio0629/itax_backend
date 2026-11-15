// Package service — Matching & Geo mantiqi: joylashuv (F-11 tezlik tekshiruvi), radius qidiruvi, taklif hodisalari, marshrut.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/matching-geo/internal/events"
	"github.com/izgo/matching-geo/internal/store"
	"github.com/izgo/shared/geo"
)

type Options struct {
	MaxSpeedKMH    float64
	MinJumpM       float64
	DefaultRadiusM float64
	MaxRadiusM     float64
	MaxCandidates  int
	OfferTTL       time.Duration
}

// OfferPublisher — offer.created ni yuboradi (NATS JetStream).
type OfferPublisher interface {
	PublishOfferCreated(ctx context.Context, o events.OfferCreated, trace string) error
}

type Service struct {
	st     *store.Store
	router geo.Router
	pub    OfferPublisher
	opt    Options
	log    *slog.Logger
	now    func() time.Time
}

func New(st *store.Store, r geo.Router, pub OfferPublisher, opt Options, log *slog.Logger) *Service {
	return &Service{st: st, router: r, pub: pub, opt: opt, log: log, now: time.Now}
}

func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// ---------------------------------------------------------------- F-11: joylashuv

// SpeedJump — oldingi nuqtadan hozirgisigacha tezlik (km/soat) va u shubhalimi. Vaqt server soati bo'yicha
// (klient vaqtiga ishonilmaydi), kamida 1 soniya deb olinadi; MinJumpM dan qisqa siljish (GPS shovqini) signal bermaydi.
func SpeedJump(prev *store.Location, lat, lng float64, now time.Time, maxKMH, minJumpM float64) (kmh float64, suspicious bool) {
	if prev == nil {
		return 0, false
	}
	dist := geo.Haversine(geo.Point{Lat: prev.Lat, Lng: prev.Lng}, geo.Point{Lat: lat, Lng: lng})
	dt := now.Sub(prev.At).Seconds()
	if dt < 1 {
		dt = 1
	}
	kmh = dist / dt * 3.6
	return kmh, dist >= minJumpM && kmh > maxKMH
}

type LocationResult struct {
	Suspicious bool
	SpeedKMH   float64
}

// UpdateLocation — GEOADD + TTL. Tezlik sakrashi (>MaxSpeedKMH) bo'lsa SIGNAL beriladi: log (WARN), Redis stream
// va javobdagi flag. Nuqta baribir saqlanadi (mock-location aniqlash — 2-bosqich).
func (s *Service) UpdateLocation(ctx context.Context, id uuid.UUID, lat, lng float64) (LocationResult, error) {
	now := s.now()
	prev, err := s.st.UpdateLocation(ctx, id, lat, lng, now)
	if err != nil {
		return LocationResult{}, err
	}
	kmh, sus := SpeedJump(prev, lat, lng, now, s.opt.MaxSpeedKMH, s.opt.MinJumpM)
	if sus {
		dist := geo.Haversine(geo.Point{Lat: prev.Lat, Lng: prev.Lng}, geo.Point{Lat: lat, Lng: lng})
		s.log.WarnContext(ctx, "F-11: GPS tezlik sakrashi", "driver_id", id, "speed_kmh", math.Round(kmh), "distance_m", math.Round(dist),
			"elapsed_s", now.Sub(prev.At).Seconds(), "limit_kmh", s.opt.MaxSpeedKMH)
		if err := s.st.RecordAnomaly(ctx, store.Anomaly{DriverID: id, SpeedKMH: kmh, DistanceM: dist, Elapsed: now.Sub(prev.At), At: now}); err != nil {
			s.log.ErrorContext(ctx, "anomaliyani yozib bo'lmadi", "driver_id", id, "err", err)
		}
	}
	return LocationResult{Suspicious: sus, SpeedKMH: kmh}, nil
}

// ---------------------------------------------------------------- qidiruv

type FindRequest struct {
	Kind        store.OrderKind
	Seats       *int
	CarClass    *string
	VehicleType *string
	From        geo.Point
	RadiusM     *float64
	Limit       int
	// OrderID berilsa: har bir mos haydovchi uchun taklif yaratiladi (offer_id deterministik) va offer.created chiqadi.
	OrderID   *uuid.UUID
	ExpiresAt *time.Time
	TraceID   string
}

type Match struct {
	DriverID       uuid.UUID
	DistanceM      int
	Location       geo.Point
	PickupETAs     int
	OfferID        *uuid.UUID
	OfferExpiresAt *time.Time
}

// offerNS — offer_id = UUIDv5(offerNS, order_id + driver_id): bir xil (buyurtma, haydovchi) uchun takroriy chaqiruv
// bir xil taklif beradi (idempotent; Trip & Order dagi UNIQUE(order_id, driver_id) bilan mos).
var offerNS = uuid.MustParse("6c0a1e4e-5b7e-4f6e-9a55-0d1f7a3c2b10")

func OfferID(order, driver uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(offerNS, []byte(order.String()+":"+driver.String()))
}

func (s *Service) Find(ctx context.Context, r FindRequest) ([]Match, error) {
	radius := s.opt.DefaultRadiusM
	if r.RadiusM != nil {
		radius = *r.RadiusM
	}
	limit := r.Limit
	if limit <= 0 {
		limit = 20
	}
	cands, err := s.st.Find(ctx, store.Query{
		Lat: r.From.Lat, Lng: r.From.Lng, RadiusM: radius, Kind: r.Kind, Seats: r.Seats, CarClass: r.CarClass,
		VehicleType: r.VehicleType, Limit: limit, MaxCandidates: s.opt.MaxCandidates,
	})
	if err != nil {
		return nil, err
	}

	now := s.now()
	expires := now.Add(s.opt.OfferTTL)
	if r.ExpiresAt != nil {
		expires = *r.ExpiresAt
	}
	out := make([]Match, 0, len(cands))
	for _, c := range cands {
		pt := geo.Point{Lat: c.Lat, Lng: c.Lng}
		rt, err := s.router.Route(ctx, pt, r.From)
		if err != nil {
			return nil, fmt.Errorf("service: marshrut: %w", err)
		}
		m := Match{DriverID: c.DriverID, DistanceM: int(math.Round(c.DistanceM)), Location: pt, PickupETAs: rt.DurationS}
		if r.OrderID != nil {
			id := OfferID(*r.OrderID, c.DriverID)
			exp := expires.UTC()
			m.OfferID, m.OfferExpiresAt = &id, &exp
		}
		out = append(out, m)
	}

	if r.OrderID != nil {
		s.publishOffers(ctx, *r.OrderID, out, r.TraceID)
	}
	return out, nil
}

// publishOffers — offer.created hodisalari. NATS ishlamasa qidiruv natijasi baribir qaytadi (Trip & Order takliflarni
// o'zi saqlaydi); yuborilmagan hodisa log'ga yoziladi. Takroriy chaqiruv xavfsiz: Nats-Msg-Id = offer_id.
func (s *Service) publishOffers(ctx context.Context, order uuid.UUID, ms []Match, trace string) {
	for _, m := range ms {
		err := s.pub.PublishOfferCreated(ctx, events.OfferCreated{
			OfferID: *m.OfferID, OrderID: order, DriverID: m.DriverID, ETAs: m.PickupETAs, ExpiresAt: *m.OfferExpiresAt,
		}, trace)
		if err != nil {
			s.log.ErrorContext(ctx, "offer.created yuborilmadi: qolgan takliflar ham o'tkazib yuborildi",
				"order_id", order, "driver_id", m.DriverID, "err", err)
			return
		}
	}
}

// ---------------------------------------------------------------- marshrut (mock)

// Route — ikki nuqta orasidagi marshrut. Hozir shared/geo.HaversineRouter (Pricing bilan bir xil formula);
// keyinroq Mapbox Directions (tayyor kod: shared/geo/geo.go oxiridagi izoh).
func (s *Service) Route(ctx context.Context, from, to geo.Point) (geo.Route, error) {
	return s.router.Route(ctx, from, to)
}

// ---------------------------------------------------------------- haydovchi holati

var ErrBusy = store.ErrDriverBusy

func (s *Service) SetProfile(ctx context.Context, id uuid.UUID, p store.Profile) error {
	return s.st.SetProfile(ctx, id, p)
}

func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, st store.Status) error {
	return s.st.SetStatus(ctx, id, st)
}

// OnOrderAccepted — NATS order.accepted: haydovchi available -> busy.
func (s *Service) OnOrderAccepted(ctx context.Context, raw []byte) error {
	ref, err := events.ParseOrderAccepted(raw)
	if err != nil {
		return err
	}
	applied, err := s.st.MarkBusy(ctx, ref.DriverID, ref.TripID)
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "haydovchi band", "driver_id", ref.DriverID, "trip_id", ref.TripID, "applied", applied)
	return nil
}

// OnTripCompleted — NATS trip.completed: haydovchi busy -> available.
func (s *Service) OnTripCompleted(ctx context.Context, raw []byte) error {
	ref, err := events.ParseTripCompleted(raw)
	if err != nil {
		return err
	}
	released, err := s.st.MarkTripCompleted(ctx, ref.DriverID, ref.TripID)
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "safar tugadi", "driver_id", ref.DriverID, "trip_id", ref.TripID, "released", released)
	return nil
}

// Sweep — joylashuvi muddati o'tgan haydovchilarni geo-to'plamdan tozalaydi.
func (s *Service) Sweep(ctx context.Context) (int, error) { return s.st.Sweep(ctx) }
