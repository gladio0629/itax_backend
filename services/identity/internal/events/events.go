// Package events — NATS JetStream hodisalari (transactional outbox orqali).
//
// Hodisa foydalanuvchi bilan BIR tranzaksiyada outbox jadvaliga yoziladi, so'ng Relay uni
// NATS'ga yuboradi (kamida bir marta). Nats-Msg-Id = event_id, shuning uchun JetStream
// takrorlarni dedupe qiladi; iste'molchilar event_id bo'yicha idempotent bo'lishi kerak.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SubjectUserCreated — shared/events/user.created.json dagi x-nats-subject.
const SubjectUserCreated = "izgo.user.created.v1"

// Event — outbox'dagi bitta yozuv.
type Event struct {
	ID        uuid.UUID
	Subject   string
	Payload   []byte
	CreatedAt time.Time
}

type envelope struct {
	Event      string      `json:"event"`
	Version    int         `json:"version"`
	EventID    uuid.UUID   `json:"event_id"`
	OccurredAt time.Time   `json:"occurred_at"`
	TraceID    string      `json:"trace_id,omitempty"`
	Payload    userCreated `json:"payload"`
}

type userCreated struct {
	UserID    uuid.UUID `json:"user_id"`
	Phone     string    `json:"phone"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// NewUserCreated — user.created hodisasi (faqat passenger/driver uchun).
func NewUserCreated(userID uuid.UUID, phone, role string, createdAt, now time.Time, traceID string) (Event, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Event{}, fmt.Errorf("events: uuid: %w", err)
	}
	body, err := json.Marshal(envelope{
		Event: "user.created", Version: 1, EventID: id, OccurredAt: now.UTC(), TraceID: traceID,
		Payload: userCreated{UserID: userID, Phone: phone, Role: role, CreatedAt: createdAt.UTC()},
	})
	if err != nil {
		return Event{}, fmt.Errorf("events: marshal: %w", err)
	}
	return Event{ID: id, Subject: SubjectUserCreated, Payload: body, CreatedAt: now.UTC()}, nil
}

// Publisher — hodisani brokerga yuboradi.
type Publisher interface {
	Publish(ctx context.Context, e Event) error
}

// Outbox — yuborilmagan hodisalarni oladi, publish muvaffaqiyatli bo'lsa belgilaydi.
type Outbox interface {
	DrainOutbox(ctx context.Context, limit int, publish func(context.Context, Event) error) (int, error)
}
