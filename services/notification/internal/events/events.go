// Package events — NATS hodisalarini tushunish: qabul qiluvchi, bildirishnoma turi va MINIMAL mazmun (F-21).
// Sxemalar: shared/events/*.json (envelope: event, version, event_id, occurred_at, payload).
package events

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Tinglanadigan subject'lar. order.created va offer.created — "haydovchilarga taklif" va "yo'lovchiga buyurtma" uchun.
var Subjects = map[string]string{
	"izgo.order.created.v1":        "order.created",
	"izgo.offer.created.v1":        "offer.created",
	"izgo.order.accepted.v1":       "order.accepted",
	"izgo.trip.completed.v1":       "trip.completed",
	"izgo.wallet.limit_reached.v1": "wallet.limit_reached",
	"izgo.driver.approved.v1":      "driver.approved",
	"izgo.order.arrived.v1":        "order.arrived",
	"izgo.trip.started.v1":         "trip.started",
	"izgo.trip.cancelled.v1":       "trip.cancelled",
	"izgo.order.cancelled.v1":      "order.cancelled",
}

// ErrPoison — buzuq hodisa (qayta urinish foyda bermaydi): tashlab yuboriladi.
var ErrPoison = errors.New("events: invalid event")

// ErrNoContext — qabul qiluvchini hali aniqlab bo'lmaydi (masalan order.accepted order.created dan oldin keldi): keyinroq qayta uriniladi.
var ErrNoContext = errors.New("events: recipient context not available yet")

// Notice — bitta hodisadan chiqadigan bildirishnoma.
type Notice struct {
	EventID uuid.UUID
	Event   string
	// Recipient — uuid.Nil bo'lsa LookupOrder bo'yicha yo'lovchi topiladi (order.accepted'da yo'lovchi ID si yo'q).
	Recipient   uuid.UUID
	LookupOrder uuid.UUID
	// RememberOrder — order.created: order_id -> passenger_id eslab qolinadi.
	RememberOrder uuid.UUID
	Kind          string // order | trip | wallet | driver (openapi Notification.kind)
	// ID — push/WS dagi YAGONA identifikator (F-21): order_id, trip_id, offer_id, wallet_id yoki driver_id.
	ID uuid.UUID
	// OrderID va Status — faqat buyurtma holati o'zgarishida (WebSocket): searching -> accepted -> completed.
	OrderID *uuid.UUID
	Status  string
}

type envelope struct {
	Event   string          `json:"event"`
	Version int             `json:"version"`
	EventID string          `json:"event_id"`
	Payload json.RawMessage `json:"payload"`
}

func uid(m map[string]any, key string) (uuid.UUID, error) {
	s, _ := m[key].(string)
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: %s: %v", ErrPoison, key, err)
	}
	return id, nil
}

// Parse — hodisani bildirishnomaga aylantiradi.
func Parse(raw []byte) (Notice, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Notice{}, fmt.Errorf("%w: json: %v", ErrPoison, err)
	}
	if env.Version != 1 {
		return Notice{}, fmt.Errorf("%w: versiya %d", ErrPoison, env.Version)
	}
	eventID, err := uuid.Parse(env.EventID)
	if err != nil {
		return Notice{}, fmt.Errorf("%w: event_id: %v", ErrPoison, err)
	}
	var p map[string]any
	if err := json.Unmarshal(env.Payload, &p); err != nil || p == nil {
		return Notice{}, fmt.Errorf("%w: payload", ErrPoison)
	}
	n := Notice{EventID: eventID, Event: env.Event}
	var errs []error
	get := func(key string) uuid.UUID {
		id, err := uid(p, key)
		errs = append(errs, err)
		return id
	}
	switch env.Event {
	case "order.created":
		n.Kind, n.Recipient, n.ID = "order", get("passenger_id"), get("order_id")
		n.RememberOrder, n.OrderID, n.Status = n.ID, &n.ID, "searching"
	case "offer.created": // haydovchiga yangi taklif
		n.Kind, n.Recipient, n.ID = "order", get("driver_id"), get("offer_id")
		get("order_id")
	case "order.accepted": // yo'lovchi ID si hodisada yo'q
		n.Kind, n.ID, n.LookupOrder = "order", get("order_id"), get("order_id")
		n.OrderID, n.Status = &n.ID, "accepted"
		get("driver_id")
	case "trip.completed":
		n.Kind, n.Recipient, n.ID = "trip", get("passenger_id"), get("trip_id")
		oid := get("order_id")
		n.OrderID, n.Status = &oid, "completed"
	case "wallet.limit_reached":
		n.Kind, n.Recipient, n.ID = "wallet", get("driver_id"), get("wallet_id")
	case "driver.approved":
		n.Kind, n.Recipient, n.ID = "driver", get("driver_id"), get("driver_id")
	case "order.arrived": // haydovchi yetib keldi -> yo'lovchiga
		get("trip_id")
		get("driver_id")
		n.Kind, n.Recipient, n.ID = "order", get("passenger_id"), get("order_id")
		n.OrderID, n.Status = &n.ID, "arrived"
	case "trip.started": // safar boshlandi -> yo'lovchiga
		get("driver_id")
		n.Kind, n.Recipient, n.ID = "trip", get("passenger_id"), get("trip_id")
		oid := get("order_id")
		n.OrderID, n.Status = &oid, "in_progress"
	case "trip.cancelled":
		// haydovchi bekor qilsa -> yo'lovchiga (buyurtma qayta searching); admin bekor qilsa -> haydovchiga
		by, _ := p["cancelled_by"].(string)
		status, _ := p["order_status"].(string)
		passenger, driver := get("passenger_id"), get("driver_id")
		n.Kind, n.ID = "trip", get("trip_id")
		oid := get("order_id")
		n.OrderID = &oid
		switch by {
		case "driver":
			n.Recipient = passenger
		case "admin":
			n.Recipient = driver
		default:
			errs = append(errs, fmt.Errorf("%w: cancelled_by %q", ErrPoison, by))
		}
		switch status {
		case "searching", "cancelled":
			n.Status = status
		default:
			errs = append(errs, fmt.Errorf("%w: order_status %q", ErrPoison, status))
		}
	case "order.cancelled": // admin bekor qildi -> yo'lovchiga
		n.Kind, n.Recipient, n.ID = "order", get("passenger_id"), get("order_id")
		n.OrderID, n.Status = &n.ID, "cancelled"
	default:
		return Notice{}, fmt.Errorf("%w: noma'lum hodisa %q", ErrPoison, env.Event)
	}
	if err := errors.Join(errs...); err != nil {
		return Notice{}, err
	}
	return n, nil
}

// TitleKey / BodyKey — lokalizatsiya kalitlari (matn ilovada).
func (n Notice) TitleKey() string { return "notification." + n.Event + ".title" }
func (n Notice) BodyKey() string  { return "notification." + n.Event + ".body" }

// Params — ro'yxatdagi bildirishnoma parametrlari: faqat ID lar (F-21).
func (n Notice) Params() map[string]string {
	switch n.Event {
	case "offer.created":
		return map[string]string{"offer_id": n.ID.String()}
	case "trip.completed", "trip.started", "trip.cancelled":
		return map[string]string{"trip_id": n.ID.String(), "order_id": n.OrderID.String()}
	case "wallet.limit_reached":
		return map[string]string{"wallet_id": n.ID.String()}
	case "driver.approved":
		return map[string]string{"driver_id": n.ID.String()}
	}
	return map[string]string{"order_id": n.ID.String()}
}

// PushData — push yuklamasi (F-21): FAQAT hodisa turi va ID. Narx, manzil, ism kabi tafsilot yo'q.
func (n Notice) PushData() map[string]string {
	return map[string]string{"type": n.Event, "id": n.ID.String()}
}

// WSMessage — WebSocket xabari. Buyurtma holati o'zgarsa `status` (+ order_id) ham bor.
func (n Notice) WSMessage() map[string]string {
	m := n.PushData()
	if n.Status != "" {
		m["status"] = n.Status
		m["order_id"] = n.OrderID.String()
	}
	return m
}
