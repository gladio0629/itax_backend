// Package events — NATS hodisalari: chiqadigan offer.created va kiruvchi order.accepted / trip.completed.
// Sxemalar: shared/events/*.json (envelope: event, version, event_id, occurred_at, trace_id, payload).
package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	SubjectOfferCreated  = "izgo.offer.created.v1"
	SubjectOrderAccepted = "izgo.order.accepted.v1"
	SubjectTripCompleted = "izgo.trip.completed.v1"
)

// ErrPoison — hodisa buzuq (qayta urinish foyda bermaydi): iste'molchi uni tashlab yuboradi.
var ErrPoison = errors.New("events: invalid event")

// OfferCreated — offer.created payload'i.
type OfferCreated struct {
	OfferID   uuid.UUID `json:"offer_id"`
	OrderID   uuid.UUID `json:"order_id"`
	DriverID  uuid.UUID `json:"driver_id"`
	ETAs      int       `json:"eta_s"`
	ExpiresAt time.Time `json:"expires_at"`
}

type envelope struct {
	Event      string    `json:"event"`
	Version    int       `json:"version"`
	EventID    uuid.UUID `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
	TraceID    string    `json:"trace_id,omitempty"`
	Payload    any       `json:"payload"`
}

// BuildOfferCreated — offer.created envelope'i (JSON).
func BuildOfferCreated(o OfferCreated, now time.Time, trace string) ([]byte, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("events: uuid: %w", err)
	}
	o.ExpiresAt = o.ExpiresAt.UTC()
	return json.Marshal(envelope{Event: "offer.created", Version: 1, EventID: id, OccurredAt: now.UTC(), TraceID: trace, Payload: o})
}

// TripRef — haydovchi va safar (order.accepted va trip.completed ikkalasida ham bor).
type TripRef struct {
	DriverID uuid.UUID
	TripID   uuid.UUID
}

type inbound struct {
	Event   string `json:"event"`
	Version int    `json:"version"`
	Payload struct {
		DriverID string `json:"driver_id"`
		TripID   string `json:"trip_id"`
	} `json:"payload"`
}

func parse(raw []byte, wantEvent string) (TripRef, error) {
	var m inbound
	if err := json.Unmarshal(raw, &m); err != nil {
		return TripRef{}, fmt.Errorf("%w: json: %v", ErrPoison, err)
	}
	if m.Event != wantEvent || m.Version != 1 {
		return TripRef{}, fmt.Errorf("%w: kutilgan %s v1, kelgan %q v%d", ErrPoison, wantEvent, m.Event, m.Version)
	}
	d, err1 := uuid.Parse(m.Payload.DriverID)
	t, err2 := uuid.Parse(m.Payload.TripID)
	if err := errors.Join(err1, err2); err != nil {
		return TripRef{}, fmt.Errorf("%w: driver_id/trip_id: %v", ErrPoison, err)
	}
	return TripRef{DriverID: d, TripID: t}, nil
}

func ParseOrderAccepted(raw []byte) (TripRef, error) { return parse(raw, "order.accepted") }
func ParseTripCompleted(raw []byte) (TripRef, error) { return parse(raw, "trip.completed") }
