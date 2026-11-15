// Package events — NATS JetStream hodisalari (transactional outbox orqali).
//
// Hodisa biznes o'zgarish bilan BIR tranzaksiyada outbox jadvaliga yoziladi, so'ng Relay uni NATS'ga
// yuboradi (kamida bir marta). Nats-Msg-Id = event_id, shuning uchun JetStream takrorlarni dedupe
// qiladi; iste'molchilar (masalan Wallet) event_id bo'yicha idempotent bo'lishi kerak.
// Sxemalar: shared/events/*.json
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/domain"
)

const (
	SubjectOrderCreated  = "izgo.order.created.v1"
	SubjectOrderAccepted = "izgo.order.accepted.v1"
	SubjectTripCompleted = "izgo.trip.completed.v1"
)

// Event — outbox'dagi bitta yozuv.
type Event struct {
	ID        uuid.UUID
	Subject   string
	Payload   []byte
	CreatedAt time.Time
}

type Publisher interface {
	Publish(ctx context.Context, e Event) error
}

type Outbox interface {
	DrainOutbox(ctx context.Context, limit int, publish func(context.Context, Event) error) (int, error)
}

type envelope struct {
	Event      string    `json:"event"`
	Version    int       `json:"version"`
	EventID    uuid.UUID `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
	TraceID    string    `json:"trace_id,omitempty"`
	Payload    any       `json:"payload"`
}

func build(name, subject string, now time.Time, trace string, payload any) (Event, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Event{}, fmt.Errorf("events: uuid: %w", err)
	}
	body, err := json.Marshal(envelope{Event: name, Version: 1, EventID: id, OccurredAt: now.UTC(), TraceID: trace, Payload: payload})
	if err != nil {
		return Event{}, fmt.Errorf("events: marshal: %w", err)
	}
	return Event{ID: id, Subject: subject, Payload: body, CreatedAt: now.UTC()}, nil
}

type point struct {
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Address string  `json:"address,omitempty"`
}

func pt(p domain.Point) point { return point{Lat: p.Lat, Lng: p.Lng, Address: p.Address} }

type orderCreated struct {
	OrderID       uuid.UUID  `json:"order_id"`
	PassengerID   uuid.UUID  `json:"passenger_id"`
	Type          string     `json:"type"`
	Seats         *int       `json:"seats,omitempty"`
	CarClass      *string    `json:"car_class,omitempty"`
	VehicleType   *string    `json:"vehicle_type,omitempty"`
	From          point      `json:"from"`
	To            point      `json:"to"`
	ScheduledAt   *time.Time `json:"scheduled_at,omitempty"`
	Price         int64      `json:"price"`
	PaymentMethod string     `json:"payment_method"`
	CreatedAt     time.Time  `json:"created_at"`
}

func OrderCreated(o domain.Order, now time.Time, trace string) (Event, error) {
	p := orderCreated{
		OrderID: o.ID, PassengerID: o.PassengerID, Type: string(o.Type), Seats: o.Seats,
		From: pt(o.From), To: pt(o.To), Price: o.Price, PaymentMethod: string(o.PaymentMethod), CreatedAt: o.CreatedAt.UTC(),
	}
	if o.CarClass != nil {
		s := string(*o.CarClass)
		p.CarClass = &s
	}
	if o.VehicleType != nil {
		s := string(*o.VehicleType)
		p.VehicleType = &s
	}
	if o.ScheduledAt != nil {
		t := o.ScheduledAt.UTC()
		p.ScheduledAt = &t
	}
	return build("order.created", SubjectOrderCreated, now, trace, p)
}

type orderAccepted struct {
	OrderID    uuid.UUID `json:"order_id"`
	TripID     uuid.UUID `json:"trip_id"`
	DriverID   uuid.UUID `json:"driver_id"`
	OfferID    uuid.UUID `json:"offer_id"`
	AcceptedAt time.Time `json:"accepted_at"`
}

func OrderAccepted(orderID, tripID, driverID, offerID uuid.UUID, at time.Time, trace string) (Event, error) {
	return build("order.accepted", SubjectOrderAccepted, at, trace,
		orderAccepted{OrderID: orderID, TripID: tripID, DriverID: driverID, OfferID: offerID, AcceptedAt: at.UTC()})
}

type tripCompleted struct {
	TripID      uuid.UUID `json:"trip_id"`
	OrderID     uuid.UUID `json:"order_id"`
	DriverID    uuid.UUID `json:"driver_id"`
	PassengerID uuid.UUID `json:"passenger_id"`
	FinalPrice  int64     `json:"final_price"`
	Commission  int64     `json:"commission"`
	DistanceM   int       `json:"distance_m"`
	DurationS   int       `json:"duration_s"`
	CompletedAt time.Time `json:"completed_at"`
	// PaymentMethod — Wallet shunga qarab ajratadi: naqd — faqat komissiya yechiladi,
	// elektron (card/wallet/payme/click) — haydovchiga summa minus komissiya yoziladi.
	PaymentMethod string `json:"payment_method"`
}

// TripCompleted — Wallet shu hodisani tinglaydi (komissiya hisoblash uchun). pm — buyurtmaning to'lov usuli.
func TripCompleted(t domain.Trip, pm domain.PaymentMethod, trace string) (Event, error) {
	if pm == "" {
		return Event{}, fmt.Errorf("events: trip %s: to'lov usuli bo'sh", t.ID)
	}
	if t.CompletedAt == nil || t.FinalPrice == nil || t.Commission == nil || t.DistanceM == nil || t.DurationS == nil {
		return Event{}, fmt.Errorf("events: trip %s to'liq yakunlanmagan", t.ID)
	}
	return build("trip.completed", SubjectTripCompleted, *t.CompletedAt, trace, tripCompleted{
		TripID: t.ID, OrderID: t.OrderID, DriverID: t.DriverID, PassengerID: t.PassengerID,
		FinalPrice: *t.FinalPrice, Commission: *t.Commission, DistanceM: *t.DistanceM, DurationS: *t.DurationS,
		CompletedAt: t.CompletedAt.UTC(), PaymentMethod: string(pm),
	})
}

const (
	SubjectOrderArrived   = "izgo.order.arrived.v1"
	SubjectTripStarted    = "izgo.trip.started.v1"
	SubjectTripCancelled  = "izgo.trip.cancelled.v1"
	SubjectOrderCancelled = "izgo.order.cancelled.v1"
)

// tripRef — safar holati o'zgarishi hodisalari uchun minimal payload (ism/telefon yo'q).
type tripRef struct {
	TripID      uuid.UUID `json:"trip_id"`
	OrderID     uuid.UUID `json:"order_id"`
	DriverID    uuid.UUID `json:"driver_id"`
	PassengerID uuid.UUID `json:"passenger_id"`
	At          time.Time `json:"at"`
}

func ref(t domain.Trip, at time.Time) tripRef {
	return tripRef{TripID: t.ID, OrderID: t.OrderID, DriverID: t.DriverID, PassengerID: t.PassengerID, At: at.UTC()}
}

// OrderArrived — haydovchi olib ketish joyiga yetib keldi (yo'lovchiga xabar).
func OrderArrived(t domain.Trip, at time.Time, trace string) (Event, error) {
	return build("order.arrived", SubjectOrderArrived, at, trace, ref(t, at))
}

// TripStarted — safar boshlandi (yo'lovchiga xabar).
func TripStarted(t domain.Trip, at time.Time, trace string) (Event, error) {
	return build("trip.started", SubjectTripStarted, at, trace, ref(t, at))
}

type tripCancelled struct {
	tripRef
	// CancelledBy — driver (buyurtma qayta searching) | admin (buyurtma cancelled).
	CancelledBy string `json:"cancelled_by"`
	OrderStatus string `json:"order_status"`
}

// TripCancelled — biriktirilgan safar bekor qilindi. Matching haydovchini bo'shatadi; Notification xabar beradi.
func TripCancelled(t domain.Trip, by string, orderStatus domain.OrderStatus, at time.Time, trace string) (Event, error) {
	return build("trip.cancelled", SubjectTripCancelled, at, trace, tripCancelled{tripRef: ref(t, at), CancelledBy: by, OrderStatus: string(orderStatus)})
}

type orderCancelled struct {
	OrderID     uuid.UUID  `json:"order_id"`
	PassengerID uuid.UUID  `json:"passenger_id"`
	DriverID    *uuid.UUID `json:"driver_id"`
	CancelledBy string     `json:"cancelled_by"`
	Reason      string     `json:"reason"`
	At          time.Time  `json:"at"`
}

// OrderCancelled — buyurtma admin tomonidan bekor qilindi (yo'lovchiga xabar).
func OrderCancelled(o domain.Order, driver *uuid.UUID, by, reason string, at time.Time, trace string) (Event, error) {
	return build("order.cancelled", SubjectOrderCancelled, at, trace,
		orderCancelled{OrderID: o.ID, PassengerID: o.PassengerID, DriverID: driver, CancelledBy: by, Reason: reason, At: at.UTC()})
}

// SubjectOfferCreated — haydovchiga yangi taklif (Notification -> haydovchiga WS/push). Taklifni trip-order saqlaydi,
// shuning uchun hodisani ham shu servis chiqaradi (offer_id = qabul qilishda ishlatiladigan ID).
const SubjectOfferCreated = "izgo.offer.created.v1"

type offerCreated struct {
	OfferID   uuid.UUID `json:"offer_id"`
	OrderID   uuid.UUID `json:"order_id"`
	DriverID  uuid.UUID `json:"driver_id"`
	ETAs      int       `json:"eta_s"`
	ExpiresAt time.Time `json:"expires_at"`
}

func OfferCreated(of domain.Offer, now time.Time, trace string) (Event, error) {
	return build("offer.created", SubjectOfferCreated, now, trace,
		offerCreated{OfferID: of.ID, OrderID: of.OrderID, DriverID: of.DriverID, ETAs: of.PickupETAs, ExpiresAt: of.ExpiresAt.UTC()})
}
