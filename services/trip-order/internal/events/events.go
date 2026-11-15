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
