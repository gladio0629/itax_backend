package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/apperr"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/izgo/trip-order/internal/events"
	"github.com/izgo/trip-order/internal/guard"
	"github.com/izgo/trip-order/internal/store"
)

// ---------------------------------------------------------------- haydovchi: rad etish, yetib keldim, bekor qilish

// RejectOffer — haydovchi taklifni rad etadi (feed'dan yo'qoladi; buyurtma boshqalarga ochiq qoladi).
func (s *Service) RejectOffer(ctx context.Context, driver, offerID uuid.UUID, reason *string) error {
	err := s.st.RejectOffer(ctx, offerID, driver, reason)
	var se *domain.StateError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return apperr.OrderNotFound(offerID.String())
	case errors.As(err, &se):
		return apperr.Forbidden("offer_closed", nil)
	}
	return err
}

// ArriveTrip — "yetib keldim": safar assigned bo'lishi shart; buyurtma accepted -> arrived. Takroriy chaqiruv xato emas.
func (s *Service) ArriveTrip(ctx context.Context, driver, tripID uuid.UUID, trace string) (domain.Trip, error) {
	now := s.now()
	t, changed, err := s.st.ArriveTrip(ctx, tripID, driver, now, func(t domain.Trip) (events.Event, error) { return events.OrderArrived(t, now, trace) })
	var se *domain.StateError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.Trip{}, apperr.OrderNotFound(tripID.String())
	case errors.As(err, &se):
		return domain.Trip{}, forbiddenForTripStatus(se.Trip)
	case err != nil:
		return domain.Trip{}, err
	}
	if changed {
		s.kick()
	}
	return t, nil
}

// DriverCancelReasons — haydovchi bekor qilish sabablari.
var DriverCancelReasons = map[string]bool{"passenger_no_show": true, "passenger_asked": true, "car_problem": true, "emergency": true, "other": true}

// CancelTrip — haydovchi boshlanmagan safarni bekor qiladi; buyurtma qayta qidiruvga qaytadi (boshqa haydovchilarga takliflar).
// Boshlangan safarni bekor qilib bo'lmaydi (403 trip_started) — operator orqali.
func (s *Service) CancelTrip(ctx context.Context, driver, tripID uuid.UUID, reason string, comment *string, trace string) (domain.Trip, error) {
	trip, order, err := s.st.GetTripWithOrder(ctx, tripID, driver)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Trip{}, apperr.OrderNotFound(tripID.String())
	}
	if err != nil {
		return domain.Trip{}, err
	}
	switch trip.Status {
	case domain.TripInProgress:
		return domain.Trip{}, apperr.Forbidden("trip_started", nil)
	case domain.TripAssigned:
	default:
		return domain.Trip{}, forbiddenForTripStatus(trip.Status)
	}
	now := s.now()
	deadline := s.searchDeadline(now, scheduleBase(order))
	order.Status = domain.Searching
	var offers []domain.Offer
	cands, err := s.matcher.FindDrivers(ctx, order)
	if err != nil {
		s.log.WarnContext(ctx, "matching xatosi: buyurtma takliflarsiz qayta qidiruvga qaytadi", "order_id", order.ID, "err", err)
	}
	for _, c := range cands {
		if c.DriverID == driver {
			continue
		}
		oid, err := uuid.NewV7()
		if err != nil {
			return domain.Trip{}, fmt.Errorf("uuid: %w", err)
		}
		offers = append(offers, domain.Offer{ID: oid, OrderID: order.ID, DriverID: c.DriverID, PickupETAs: c.PickupETASeconds,
			ExpiresAt: deadline, Status: domain.OfferPending, CreatedAt: now})
	}
	t, err := s.st.DriverCancel(ctx, tripID, driver, reason, comment, now, deadline, offers, func(t domain.Trip) (events.Event, error) {
		return events.TripCancelled(t, "driver", domain.Searching, now, trace)
	}, func(of domain.Offer) (events.Event, error) { return events.OfferCreated(of, now, trace) })
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
	if rerr := s.guard.ReleaseAcceptLock(ctx, order.ID, driver); rerr != nil {
		s.log.WarnContext(ctx, "qulfni bo'shatib bo'lmadi", "order_id", order.ID, "err", rerr)
	}
	s.kick()
	return t, nil
}

// TripStatusesActive / TripStatusesHistory — GET /trips va /trips/history filtrlari.
var (
	TripStatusesActive  = []string{string(domain.TripAssigned), string(domain.TripInProgress)}
	TripStatusesHistory = []string{string(domain.TripCompleted), string(domain.TripCancelled)}
)

// Trips — haydovchining safarlari (buyurtmasi bilan).
func (s *Service) Trips(ctx context.Context, driver uuid.UUID, statuses []string, cursor string, limit int) (Page[domain.TripView], error) {
	cur, err := parseCursor(cursor)
	if err != nil {
		return Page[domain.TripView]{}, err
	}
	rows, err := s.st.ListTrips(ctx, driver, statuses, cur, limit)
	if err != nil {
		return Page[domain.TripView]{}, err
	}
	p := Page[domain.TripView]{Items: rows}
	if len(rows) > limit {
		p.Items = rows[:limit]
		last := p.Items[limit-1]
		c := store.Cursor{At: last.Trip.CreatedAt, ID: last.Trip.ID}.Encode()
		p.Cursor = &c
	}
	return p, nil
}

// ---------------------------------------------------------------- admin (ichki API, backoffice orqali)

func (s *Service) AdminOrders(ctx context.Context, f store.OrderFilter, cursor string, limit int) (Page[domain.Order], error) {
	cur, err := parseCursor(cursor)
	if err != nil {
		return Page[domain.Order]{}, err
	}
	rows, err := s.st.ListAllOrders(ctx, f, cur, limit)
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

func (s *Service) AdminOrder(ctx context.Context, id uuid.UUID) (domain.Order, error) {
	o, err := s.st.GetOrder(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Order{}, apperr.OrderNotFound(id.String())
	}
	return o, err
}

type AdminUpdate struct {
	Comment       *string
	PaymentMethod *domain.PaymentMethod
	ScheduledAt   *time.Time
	ScheduledFrom *time.Time
	ScheduledTo   *time.Time
}

// AdminUpdateOrder — faqat searching; narxga ta'sir qilmaydigan maydonlar (narx o'zgarmaydi).
func (s *Service) AdminUpdateOrder(ctx context.Context, id uuid.UUID, in AdminUpdate) (domain.Order, error) {
	for attempt := 0; attempt < 3; attempt++ {
		cur, err := s.AdminOrder(ctx, id)
		if err != nil {
			return domain.Order{}, err
		}
		if cur.Status != domain.Searching {
			return domain.Order{}, forbiddenForOrderStatus(cur.Status)
		}
		now := s.now()
		o := cur
		if in.Comment != nil {
			o.Comment = in.Comment
		}
		if in.PaymentMethod != nil {
			o.PaymentMethod = *in.PaymentMethod
		}
		if in.ScheduledAt != nil || in.ScheduledFrom != nil || in.ScheduledTo != nil {
			if err := s.checkTiming(now, in.ScheduledAt, in.ScheduledFrom, in.ScheduledTo); err != nil {
				return domain.Order{}, err
			}
			o.ScheduledAt, o.ScheduledFrom, o.ScheduledTo = in.ScheduledAt, in.ScheduledFrom, in.ScheduledTo
			o.SearchExpiresAt = s.searchDeadline(now, scheduleBase(o))
		}
		ok, err := s.st.AdminUpdate(ctx, o, cur.Version, now)
		if err != nil {
			return domain.Order{}, err
		}
		if ok {
			return s.AdminOrder(ctx, id)
		}
	}
	return domain.Order{}, apperr.Forbidden("concurrent_update", nil)
}

// AdminCancelReasons — admin bekor qilish sabablari.
var AdminCancelReasons = map[string]bool{"admin_decision": true, "passenger_request": true, "driver_request": true, "duplicate": true, "fraud": true, "other": true}

// AdminCancelOrder — searching/accepted/arrived -> cancelled. Boshlangan safar — 403 trip_started.
func (s *Service) AdminCancelOrder(ctx context.Context, id uuid.UUID, reason string, comment *string, trace string) (domain.Order, error) {
	now := s.now()
	o, trip, changed, err := s.st.AdminCancel(ctx, id, reason, comment, now, func(o domain.Order, t *domain.Trip) ([]events.Event, error) {
		var drv *uuid.UUID
		var out []events.Event
		if t != nil {
			drv = &t.DriverID
			ev, err := events.TripCancelled(*t, "admin", domain.Cancelled, now, trace)
			if err != nil {
				return nil, err
			}
			out = append(out, ev)
		}
		ev, err := events.OrderCancelled(o, drv, "admin", reason, now, trace)
		return append(out, ev), err
	})
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
		s.release(ctx, guard.Passenger, o.PassengerID)
		if trip != nil {
			s.release(ctx, guard.Driver, trip.DriverID)
			if rerr := s.guard.ReleaseAcceptLock(ctx, o.ID, trip.DriverID); rerr != nil {
				s.log.WarnContext(ctx, "qulfni bo'shatib bo'lmadi", "order_id", o.ID, "err", rerr)
			}
		}
		s.kick()
	}
	return o, nil
}

// StatsTZ — kunlik guruhlash uchun vaqt mintaqasi (O'zbekiston, UTC+5).
const StatsTZ = 5 * time.Hour

func (s *Service) AdminStats(ctx context.Context, from, to *time.Time) (store.Stats, error) {
	return s.st.OrderStats(ctx, from, to, StatsTZ)
}
