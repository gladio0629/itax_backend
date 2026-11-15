// Package service — Trip & Order biznes logikasi.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/apperr"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/izgo/trip-order/internal/events"
	"github.com/izgo/trip-order/internal/external"
	"github.com/izgo/trip-order/internal/guard"
	"github.com/izgo/trip-order/internal/store"
)

type Options struct {
	SearchTimeout        time.Duration
	AcceptLockTTL        time.Duration
	MaxScheduleAhead     time.Duration
	MinScheduleRange     time.Duration
	MaxScheduleRange     time.Duration
	QuoteTTL             time.Duration
	PassengerActiveLimit int
	DriverActiveLimit    int
}

type Service struct {
	opt     Options
	st      *store.Store
	guard   *guard.Guard
	pricer  external.Pricer
	wallet  external.Wallet
	matcher external.Matcher
	log     *slog.Logger
	now     func() time.Time
	onEvent func()
	cat     catalogCache
}

func New(opt Options, st *store.Store, g *guard.Guard, p external.Pricer, w external.Wallet, m external.Matcher, log *slog.Logger) *Service {
	return &Service{opt: opt, st: st, guard: g, pricer: p, wallet: w, matcher: m, log: log, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock — testlar uchun soatni almashtiradi.
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// OnEvent — outbox'ga hodisa yozilgach relay'ni uyg'otadi.
func (s *Service) OnEvent(f func()) { s.onEvent = f }

func (s *Service) kick() {
	if s.onEvent != nil {
		s.onEvent()
	}
}

// ---------------------------------------------------------------- yordamchilar

func forbiddenForOrderStatus(st domain.OrderStatus) *apperr.Error {
	switch st {
	case domain.Accepted, domain.Arrived:
		return apperr.Forbidden("driver_assigned", nil)
	case domain.InProgress:
		return apperr.Forbidden("trip_started", nil)
	default:
		return apperr.Forbidden("order_closed", nil)
	}
}

func sameLocation(a, b domain.Point) bool { return a.Lat == b.Lat && a.Lng == b.Lng }

func (s *Service) searchDeadline(now time.Time, scheduled *time.Time) time.Time {
	base := now
	if scheduled != nil && scheduled.After(now) {
		base = *scheduled
	}
	return base.Add(s.opt.SearchTimeout)
}

func (s *Service) checkSchedule(now time.Time, t *time.Time) error {
	if t == nil {
		return nil
	}
	if !t.After(now) {
		return apperr.Validation(map[string]string{"scheduled_at": "in_past"})
	}
	if t.After(now.Add(s.opt.MaxScheduleAhead)) {
		return apperr.Validation(map[string]string{"scheduled_at": "too_far"})
	}
	return nil
}

// reserve — F-24 uchun TEZ OLDINDAN-TEKSHIRUV (maslahat): Redis hisoblagichida joy band qiladi. Haqiqat manbai —
// bazadagi tranzaksiya ichidagi sanash (Store.CreateOrder / Store.AcceptOffer, advisory lock ostida): parallel
// so'rovlar hisoblagichni pastga tushirib yuborsa ham limit buzilmaydi. Hisoblagich yo'q bo'lsa (Redis yo'qolgan / birinchi marta)
// yoki limitga yetgan ko'ringanda bazadagi haqiqiy son bilan tiklanadi — shu tufayli Redis tozalansa ham
// yoki hisoblagich "sizib" yuqori qolsa ham cheklov to'g'ri ishlaydi.
func (s *Service) reserve(ctx context.Context, k guard.Kind, id uuid.UUID, limit int, count func(context.Context) (int, error)) (bool, error) {
	r, err := s.guard.Reserve(ctx, k, id, limit)
	if err != nil || r == guard.Reserved {
		return r == guard.Reserved, err
	}
	actual, err := count(ctx)
	if err != nil {
		return false, fmt.Errorf("faol sonni sanash: %w", err)
	}
	if err := s.guard.Heal(ctx, k, id, actual); err != nil {
		return false, err
	}
	if actual >= limit {
		return false, nil
	}
	r, err = s.guard.Reserve(ctx, k, id, limit)
	return r == guard.Reserved, err
}

func (s *Service) release(ctx context.Context, k guard.Kind, id uuid.UUID) {
	if err := s.guard.Release(ctx, k, id); err != nil {
		s.log.WarnContext(ctx, "hisoblagichni kamaytirib bo'lmadi", "kind", k, "id", id, "err", err)
	}
}

// ---------------------------------------------------------------- buyurtma yaratish

type CreateInput struct {
	Type          domain.OrderType
	Seats         *int
	SeatIDs       []domain.SeatID
	CarClass      *domain.CarClass
	VehicleType   *domain.VehicleType
	From, To      domain.Point
	ScheduledAt   *time.Time
	ScheduledFrom *time.Time
	ScheduledTo   *time.Time
	PaymentMethod domain.PaymentMethod
	Comment       *string
	// QuoteID — POST /orders/quote natijasi: buyurtma aynan shu narx bilan yaratiladi.
	QuoteID *uuid.UUID
}

func (in CreateInput) order(passenger uuid.UUID) domain.Order {
	return domain.Order{
		PassengerID: passenger, Type: in.Type, Seats: in.Seats, SeatIDs: in.SeatIDs, CarClass: in.CarClass, VehicleType: in.VehicleType,
		From: in.From, To: in.To, ScheduledAt: in.ScheduledAt, ScheduledFrom: in.ScheduledFrom, ScheduledTo: in.ScheduledTo,
		PaymentMethod: in.PaymentMethod, Comment: in.Comment, QuoteID: in.QuoteID,
	}
}

func (s *Service) CreateOrder(ctx context.Context, passenger uuid.UUID, in CreateInput, idemKey *string, trace string) (domain.Order, error) {
	now := s.now()
	id, err := uuid.NewV7()
	if err != nil {
		return domain.Order{}, fmt.Errorf("uuid: %w", err)
	}
	o := in.order(passenger)
	o.ID, o.Status, o.IdempotencyKey, o.CreatedAt, o.UpdatedAt = id, domain.Searching, idemKey, now, now
	o.SearchExpiresAt = s.searchDeadline(now, scheduleBase(o))
	if err := s.validateNew(now, o); err != nil {
		return domain.Order{}, err
	}

	// Idempotency-Key: takroriy so'rov mavjud buyurtmani qaytaradi (limit ham, narx ham qayta hisoblanmaydi).
	if idemKey != nil {
		if ex, err := s.st.GetOrderByIdempotency(ctx, passenger, *idemKey); err == nil {
			return ex, nil
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.Order{}, err
		}
	}

	// Narx: quote_id bo'lsa — aynan taklifdagi narx (mos kelmasa aniq xato); bo'lmasa — hozirgi tarif bo'yicha.
	if o.QuoteID != nil {
		q, err := s.quoted(ctx, passenger, o, now)
		if err != nil {
			return domain.Order{}, err
		}
		o.Price, o.Commission, o.DistanceM, o.DurationS, o.Lines = q.Price, q.Commission, q.DistanceM, q.DurationS, q.Lines
	} else {
		q, err := s.price(ctx, o)
		if err != nil {
			return domain.Order{}, err
		}
		o.Price, o.Commission, o.DistanceM, o.DurationS, o.Lines = q.Price, q.Commission, q.DistanceM, q.DurationS, q.Lines
	}
	o.FromCityID, o.ToCityID = s.cityOf(ctx, o.From), s.cityOf(ctx, o.To)

	ok, err := s.reserve(ctx, guard.Passenger, passenger, s.opt.PassengerActiveLimit,
		func(c context.Context) (int, error) { return s.st.CountActiveOrders(c, passenger) })
	if err != nil {
		return domain.Order{}, err
	}
	if !ok {
		return domain.Order{}, apperr.Forbidden("active_orders_limit", map[string]any{"limit": s.opt.PassengerActiveLimit})
	}

	// Matching (real yoki mock): xato buyurtmani to'xtatmaydi — buyurtma takliflarsiz qoladi va muddati o'tadi.
	var offers []domain.Offer
	cands, err := s.matcher.FindDrivers(ctx, o)
	if err != nil {
		s.log.WarnContext(ctx, "matching xatosi: buyurtma takliflarsiz yaratiladi", "order_id", o.ID, "err", err)
	}
	for _, c := range cands {
		oid, err := uuid.NewV7()
		if err != nil {
			s.release(ctx, guard.Passenger, passenger)
			return domain.Order{}, fmt.Errorf("uuid: %w", err)
		}
		offers = append(offers, domain.Offer{ID: oid, OrderID: o.ID, DriverID: c.DriverID, PickupETAs: c.PickupETASeconds,
			ExpiresAt: o.SearchExpiresAt, Status: domain.OfferPending, CreatedAt: now})
	}

	ev, err := events.OrderCreated(o, now, trace)
	if err != nil {
		s.release(ctx, guard.Passenger, passenger)
		return domain.Order{}, err
	}
	created, replay, err := s.st.CreateOrder(ctx, o, offers, ev, s.opt.PassengerActiveLimit)
	if err != nil || replay {
		s.release(ctx, guard.Passenger, passenger) // joy yaratilmadi
		if errors.Is(err, domain.ErrLimitReached) {
			return domain.Order{}, apperr.Forbidden("active_orders_limit", map[string]any{"limit": s.opt.PassengerActiveLimit})
		}
		if errors.Is(err, domain.ErrQuoteUsed) {
			return domain.Order{}, apperr.QuoteAlreadyUsed(o.QuoteID.String())
		}
		if err != nil {
			return domain.Order{}, err
		}
		return created, nil
	}
	s.kick()
	return created, nil
}

// ---------------------------------------------------------------- o'qish

// Actor — token egasi.
type Actor struct {
	ID       uuid.UUID
	IsDriver bool
}

// GetOrder (F-10): buyurtmani faqat egasi (yo'lovchi) yoki unga biriktirilgan haydovchi ko'radi; boshqalarga 404.
func (s *Service) GetOrder(ctx context.Context, a Actor, id uuid.UUID) (domain.Order, error) {
	o, err := s.st.GetOrder(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Order{}, apperr.OrderNotFound(id.String())
	}
	if err != nil {
		return domain.Order{}, err
	}
	owner := !a.IsDriver && o.PassengerID == a.ID
	assigned := a.IsDriver && o.DriverID != nil && *o.DriverID == a.ID
	if !owner && !assigned {
		return domain.Order{}, apperr.OrderNotFound(id.String())
	}
	return o, nil
}

type Page[T any] struct {
	Items  []T
	Cursor *string
}

func parseCursor(c string) (*store.Cursor, error) {
	if c == "" {
		return nil, nil
	}
	cur, err := store.DecodeCursor(c)
	if err != nil {
		return nil, apperr.Validation(map[string]string{"cursor": "invalid"})
	}
	return &cur, nil
}

func (s *Service) History(ctx context.Context, passenger uuid.UUID, cursor string, limit int) (Page[domain.Order], error) {
	cur, err := parseCursor(cursor)
	if err != nil {
		return Page[domain.Order]{}, err
	}
	rows, err := s.st.ListOrders(ctx, passenger, cur, limit)
	if err != nil {
		return Page[domain.Order]{}, err
	}
	p := Page[domain.Order]{Items: rows}
	if len(rows) > limit {
		p.Items = rows[:limit]
		last := p.Items[limit-1]
		c := store.Cursor{At: last.CreatedAt, ID: last.ID}.Encode()
		p.Cursor = &c
	}
	return p, nil
}

func (s *Service) Offers(ctx context.Context, driver uuid.UUID, cursor string, limit int) (Page[domain.OfferView], error) {
	cur, err := parseCursor(cursor)
	if err != nil {
		return Page[domain.OfferView]{}, err
	}
	rows, err := s.st.ListOffers(ctx, driver, s.now(), cur, limit)
	if err != nil {
		return Page[domain.OfferView]{}, err
	}
	p := Page[domain.OfferView]{Items: rows}
	if len(rows) > limit {
		p.Items = rows[:limit]
		last := p.Items[limit-1]
		c := store.Cursor{At: last.Offer.CreatedAt, ID: last.Offer.ID}.Encode()
		p.Cursor = &c
	}
	return p, nil
}

// ---------------------------------------------------------------- o'zgartirish / bekor qilish

type UpdateInput struct {
	Seats         *int
	SeatIDs       []domain.SeatID
	ScheduledFrom *time.Time
	ScheduledTo   *time.Time
	CarClass      *domain.CarClass
	VehicleType   *domain.VehicleType
	To            *domain.Point
	ScheduledAt   *time.Time
	PaymentMethod *domain.PaymentMethod
	Comment       *string
}

// UpdateOrder — faqat status=searching bo'lsagina; narx qayta hisoblanadi. Aks holda 403 FORBIDDEN (params.reason).
func (s *Service) UpdateOrder(ctx context.Context, passenger, id uuid.UUID, in UpdateInput) (domain.Order, error) {
	for attempt := 0; attempt < 3; attempt++ {
		cur, err := s.st.GetOrder(ctx, id)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && cur.PassengerID != passenger) {
			return domain.Order{}, apperr.OrderNotFound(id.String())
		}
		if err != nil {
			return domain.Order{}, err
		}
		if cur.Status != domain.Searching {
			return domain.Order{}, forbiddenForOrderStatus(cur.Status)
		}
		now := s.now()

		o := cur
		if (in.Seats != nil || in.SeatIDs != nil) && in.CarClass != nil {
			return domain.Order{}, apperr.Validation(map[string]string{"car_class": "conflicts_with_seats"})
		}
		if in.Seats != nil {
			o.Seats, o.SeatIDs, o.CarClass = in.Seats, nil, nil
		}
		if in.SeatIDs != nil {
			n := len(in.SeatIDs)
			o.Seats, o.SeatIDs, o.CarClass = &n, in.SeatIDs, nil
		}
		if in.CarClass != nil {
			o.CarClass, o.Seats, o.SeatIDs = in.CarClass, nil, nil
		}
		if in.VehicleType != nil {
			o.VehicleType = in.VehicleType
		}
		if in.To != nil {
			o.To = *in.To
			o.ToCityID = s.cityOf(ctx, o.To)
		}
		if in.ScheduledAt != nil || in.ScheduledFrom != nil || in.ScheduledTo != nil {
			if err := s.checkTiming(now, in.ScheduledAt, in.ScheduledFrom, in.ScheduledTo); err != nil {
				return domain.Order{}, err
			}
			o.ScheduledAt, o.ScheduledFrom, o.ScheduledTo = in.ScheduledAt, in.ScheduledFrom, in.ScheduledTo
		}
		if in.PaymentMethod != nil {
			o.PaymentMethod = *in.PaymentMethod
		}
		if in.Comment != nil {
			o.Comment = in.Comment
		}
		if err := o.CheckShape(); err != nil {
			f := map[string]string{"body": "invalid_combination"}
			switch {
			case o.Type == domain.TypeDelivery && (in.Seats != nil || in.SeatIDs != nil || in.CarClass != nil):
				f = map[string]string{"seats": "not_applicable_to_delivery"}
				if in.CarClass != nil {
					f = map[string]string{"car_class": "not_applicable_to_delivery"}
				}
			case o.Type == domain.TypePassenger && in.VehicleType != nil:
				f = map[string]string{"vehicle_type": "not_applicable_to_passenger"}
			}
			return domain.Order{}, apperr.Validation(f)
		}
		if sameLocation(o.From, o.To) {
			return domain.Order{}, apperr.Validation(map[string]string{"to": "same_as_from"})
		}

		q, err := s.price(ctx, o)
		if err != nil {
			return domain.Order{}, err
		}
		o.Price, o.Commission, o.DistanceM, o.DurationS, o.Lines = q.Price, q.Commission, q.DistanceM, q.DurationS, q.Lines
		o.SearchExpiresAt = s.searchDeadline(now, scheduleBase(o))

		ok, err := s.st.UpdateSearching(ctx, o, cur.Version, now)
		if err != nil {
			return domain.Order{}, err
		}
		if ok {
			return s.st.GetOrder(ctx, id)
		}
		// versiya o'zgargan yoki holat o'zgargan: qayta o'qib, qaror chiqaramiz
	}
	return domain.Order{}, apperr.Forbidden("concurrent_update", nil)
}

// CancelOrder: searching -> cancelled. Takroriy chaqiruv xato bermaydi (idempotent).
func (s *Service) CancelOrder(ctx context.Context, passenger, id uuid.UUID, reason, comment *string) (domain.Order, error) {
	o, changed, err := s.st.CancelOrder(ctx, id, passenger, reason, comment, s.now())
	var se *domain.StateError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.Order{}, apperr.OrderNotFound(id.String())
	case errors.As(err, &se):
		return domain.Order{}, forbiddenForOrderStatus(se.Order)
	case err != nil:
		return domain.Order{}, err
	}
	if changed {
		s.release(ctx, guard.Passenger, passenger)
	}
	return o, nil
}

// Sweep — qidiruv muddati o'tgan buyurtmalarni expired qiladi va yo'lovchi hisoblagichini bo'shatadi.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	expired, err := s.st.ExpireStale(ctx, s.now())
	if err != nil {
		return 0, err
	}
	for _, o := range expired {
		s.release(ctx, guard.Passenger, o.PassengerID)
	}
	return len(expired), nil
}

// ---------------------------------------------------------------- haydovchi: qabul, boshlash, tugatish

// AcceptOffer — "birinchi qabul qilgan oladi". Tartib:
//  1. taklif shu haydovchiniki ekanini tekshirish (F-10),
//  2. Wallet tekshiruvi (F-13) va faol safar limiti (F-24, haydovchi uchun 1) — qulfdan OLDIN,
//  3. Redis SETNX qulfi: yutqazgan haydovchi darhol ORDER_ALREADY_TAKEN oladi,
//  4. bazada atomar UPDATE ... WHERE status='searching' (qulf yo'qolsa/muddati tugasa ham ikkinchi marta olinmaydi).
func (s *Service) AcceptOffer(ctx context.Context, driver, offerID uuid.UUID, trace string) (domain.Trip, error) {
	view, err := s.st.GetOffer(ctx, offerID, driver)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Trip{}, apperr.OrderNotFound(offerID.String())
	}
	if err != nil {
		return domain.Trip{}, err
	}
	orderID := view.Order.ID
	mk := func(oid, tid uuid.UUID) (events.Event, error) {
		return events.OrderAccepted(oid, tid, driver, offerID, s.now(), trace)
	}

	// Qayta urinish: bu haydovchi allaqachon olgan — mavjud safar qaytadi (limitlar qayta tekshirilmaydi).
	if view.Offer.Status == domain.OfferAccepted {
		trip, err := s.st.AcceptOffer(ctx, offerID, driver, s.now(), s.opt.DriverActiveLimit, mk)
		return s.mapAcceptErr(trip, err, orderID)
	}
	switch view.Order.Status {
	case domain.Accepted, domain.Arrived, domain.InProgress, domain.Completed:
		return domain.Trip{}, apperr.OrderAlreadyTaken(orderID.String())
	case domain.Cancelled, domain.Expired:
		return domain.Trip{}, apperr.OrderNotFound(orderID.String())
	}
	if view.Offer.Status != domain.OfferPending || !view.Offer.ExpiresAt.After(s.now()) {
		return domain.Trip{}, apperr.OrderNotFound(orderID.String())
	}

	ws, err := s.wallet.CheckDriver(ctx, driver)
	if err != nil {
		return domain.Trip{}, fmt.Errorf("wallet: %w", err)
	}
	if ws.LimitReached {
		return domain.Trip{}, apperr.WalletLimitReached(ws.Limit)
	}

	ok, err := s.reserve(ctx, guard.Driver, driver, s.opt.DriverActiveLimit,
		func(c context.Context) (int, error) { return s.st.CountActiveTrips(c, driver) })
	if err != nil {
		return domain.Trip{}, err
	}
	if !ok {
		return domain.Trip{}, apperr.Forbidden("active_trip_limit", map[string]any{"limit": s.opt.DriverActiveLimit})
	}

	locked, err := s.guard.TryAcceptLock(ctx, orderID, driver, s.opt.AcceptLockTTL)
	if err != nil {
		s.release(ctx, guard.Driver, driver)
		return domain.Trip{}, err
	}
	if !locked {
		s.release(ctx, guard.Driver, driver)
		return domain.Trip{}, apperr.OrderAlreadyTaken(orderID.String())
	}

	trip, err := s.st.AcceptOffer(ctx, offerID, driver, s.now(), s.opt.DriverActiveLimit, mk)
	if err != nil {
		// Muvaffaqiyatsiz: qulf va joy bo'shatiladi, boshqa haydovchi (agar kerak bo'lsa) urinib ko'ra oladi.
		if rerr := s.guard.ReleaseAcceptLock(ctx, orderID, driver); rerr != nil {
			s.log.WarnContext(ctx, "qulfni bo'shatib bo'lmadi", "order_id", orderID, "err", rerr)
		}
		s.release(ctx, guard.Driver, driver)
		return s.mapAcceptErr(domain.Trip{}, err, orderID)
	}
	s.kick()
	return trip, nil
}

func (s *Service) mapAcceptErr(trip domain.Trip, err error, orderID uuid.UUID) (domain.Trip, error) {
	switch {
	case err == nil:
		return trip, nil
	case errors.Is(err, domain.ErrAlreadyTaken):
		return domain.Trip{}, apperr.OrderAlreadyTaken(orderID.String())
	case errors.Is(err, domain.ErrNotFound):
		return domain.Trip{}, apperr.OrderNotFound(orderID.String())
	case errors.Is(err, domain.ErrLimitReached):
		return domain.Trip{}, apperr.Forbidden("active_trip_limit", map[string]any{"limit": s.opt.DriverActiveLimit})
	}
	return domain.Trip{}, err
}

func forbiddenForTripStatus(st domain.TripStatus) *apperr.Error {
	switch st {
	case domain.TripInProgress:
		return apperr.Forbidden("already_started", nil)
	case domain.TripCompleted:
		return apperr.Forbidden("trip_completed", nil)
	case domain.TripAssigned:
		return apperr.Forbidden("trip_not_started", nil)
	default:
		return apperr.Forbidden("invalid_state", nil)
	}
}

func (s *Service) StartTrip(ctx context.Context, driver, tripID uuid.UUID, trace string) (domain.Trip, error) {
	now := s.now()
	t, err := s.st.StartTrip(ctx, tripID, driver, now, func(t domain.Trip) (events.Event, error) { return events.TripStarted(t, now, trace) })
	var se *domain.StateError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.Trip{}, apperr.OrderNotFound(tripID.String())
	case errors.As(err, &se):
		return domain.Trip{}, forbiddenForTripStatus(se.Trip)
	case err != nil:
		return domain.Trip{}, err
	}
	s.kick()
	return t, nil
}

func (s *Service) CompleteTrip(ctx context.Context, driver, tripID uuid.UUID, trace string) (domain.Trip, error) {
	trip, order, err := s.st.GetTripWithOrder(ctx, tripID, driver)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Trip{}, apperr.OrderNotFound(tripID.String())
	}
	if err != nil {
		return domain.Trip{}, err
	}
	if trip.Status != domain.TripInProgress || trip.StartedAt == nil {
		return domain.Trip{}, forbiddenForTripStatus(trip.Status)
	}

	now := s.now()
	fin, err := s.pricer.Finalize(ctx, external.FinalizeRequest{
		OrderID: order.ID, Quoted: external.Quote{Price: order.Price, Commission: order.Commission, DistanceM: order.DistanceM, DurationS: order.DurationS},
		StartedAt: *trip.StartedAt, CompletedAt: now,
	})
	if err != nil {
		return domain.Trip{}, fmt.Errorf("pricing: %w", err)
	}

	done, err := s.st.CompleteTrip(ctx, tripID, driver, store.FinalTrip{Price: fin.Price, Commission: fin.Commission, DistanceM: fin.DistanceM}, now,
		func(t domain.Trip) (events.Event, error) { return events.TripCompleted(t, order.PaymentMethod, trace) })
	var se *domain.StateError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.Trip{}, apperr.OrderNotFound(tripID.String())
	case errors.As(err, &se):
		return domain.Trip{}, forbiddenForTripStatus(se.Trip)
	case err != nil:
		return domain.Trip{}, err
	}
	s.release(ctx, guard.Driver, driver)
	s.release(ctx, guard.Passenger, order.PassengerID)
	s.kick()
	return done, nil
}

// ---------------------------------------------------------------- baholash

type RatingInput struct {
	Stars      int
	Kind       *domain.ReasonKind
	CustomText *string
}

func (s *Service) ownedOrder(ctx context.Context, passenger, id uuid.UUID) (domain.Order, error) {
	o, err := s.st.GetOrder(ctx, id)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && o.PassengerID != passenger) {
		return domain.Order{}, apperr.OrderNotFound(id.String())
	}
	return o, err
}

func (s *Service) Rate(ctx context.Context, passenger, orderID uuid.UUID, in RatingInput) (domain.Rating, error) {
	if f := domain.ValidateRating(in.Stars, in.Kind, in.CustomText); len(f) > 0 {
		return domain.Rating{}, apperr.Validation(f)
	}
	o, err := s.ownedOrder(ctx, passenger, orderID)
	if err != nil {
		return domain.Rating{}, err
	}
	if o.Status != domain.Completed || o.DriverID == nil {
		return domain.Rating{}, apperr.Forbidden("order_not_completed", nil)
	}
	r, err := s.st.CreateRating(ctx, domain.Rating{
		OrderID: orderID, PassengerID: passenger, DriverID: *o.DriverID, Stars: in.Stars, Kind: in.Kind, CustomText: in.CustomText, CreatedAt: s.now(),
	})
	if errors.Is(err, domain.ErrAlreadyRated) {
		return domain.Rating{}, apperr.Forbidden("already_rated", nil)
	}
	return r, err
}

func (s *Service) UpdateRating(ctx context.Context, passenger, orderID uuid.UUID, in RatingInput) (domain.Rating, error) {
	if f := domain.ValidateRating(in.Stars, in.Kind, in.CustomText); len(f) > 0 {
		return domain.Rating{}, apperr.Validation(f)
	}
	if _, err := s.ownedOrder(ctx, passenger, orderID); err != nil {
		return domain.Rating{}, err
	}
	r, err := s.st.UpdateRating(ctx, domain.Rating{OrderID: orderID, Stars: in.Stars, Kind: in.Kind, CustomText: in.CustomText}, s.now())
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Rating{}, apperr.New(404, "NOT_FOUND", map[string]any{"id": orderID.String()})
	}
	return r, err
}

// ---------------------------------------------------------------- fon vazifalar

// RunSweeper — muddati o'tgan buyurtmalarni davriy yopadi (ctx bekor bo'lguncha).
func (s *Service) RunSweeper(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	purge := time.NewTicker(time.Hour)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-purge.C:
			if n, err := s.st.PurgeOutbox(ctx, s.now()); err != nil && ctx.Err() == nil {
				s.log.Warn("outbox tozalanmadi", "err", err)
			} else if n > 0 {
				s.log.Info("eski outbox yozuvlari tozalandi", "count", n)
			}
			if n, err := s.st.PurgeQuotes(ctx, s.now()); err != nil && ctx.Err() == nil {
				s.log.Warn("eski narx takliflari tozalanmadi", "err", err)
			} else if n > 0 {
				s.log.Info("eski narx takliflari tozalandi", "count", n)
			}
		case <-t.C:
			if n, err := s.Sweep(ctx); err != nil {
				if ctx.Err() == nil {
					s.log.Warn("muddati o'tgan buyurtmalarni yopib bo'lmadi", "err", err)
				}
			} else if n > 0 {
				s.log.Info("qidiruv muddati o'tdi: buyurtmalar expired", "count", n)
			}
		}
	}
}
