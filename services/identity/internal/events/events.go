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

// x-nats-subject lar (shared/events/*.json). Hammasi izgo.user.> ostida — USER_EVENTS stream'i qamrab oladi.
const (
	SubjectUserCreated    = "izgo.user.created.v1"
	SubjectStaffLoggedIn  = "izgo.user.staff_logged_in.v1"
	SubjectStaffLoggedOut = "izgo.user.staff_logged_out.v1"
)

// staffLogoutNS — staff.logged_out hodisasi ID si sessiyadan deterministik (UUIDv5) olinadi: takroriy
// (idempotent) logout bir xil event_id beradi, JetStream va Backoffice audit uni bir marta yozadi.
var staffLogoutNS = uuid.MustParse("6f1c2a4e-8d3b-5e7f-9a10-2b4c6d8e0f12")

// Event — outbox'dagi bitta yozuv.
type Event struct {
	ID        uuid.UUID
	Subject   string
	Payload   []byte
	CreatedAt time.Time
}

type envelope struct {
	Event      string    `json:"event"`
	Version    int       `json:"version"`
	EventID    uuid.UUID `json:"event_id"`
	OccurredAt time.Time `json:"occurred_at"`
	TraceID    string    `json:"trace_id,omitempty"`
	Payload    any       `json:"payload"`
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
	return build(id, "user.created", SubjectUserCreated, now, traceID,
		userCreated{UserID: userID, Phone: phone, Role: role, CreatedAt: createdAt.UTC()})
}

type staffLoggedIn struct {
	StaffID    uuid.UUID `json:"staff_id"`
	Role       string    `json:"role"`
	SessionID  string    `json:"session_id"`
	Method     string    `json:"method"`
	LoggedInAt time.Time `json:"logged_in_at"`
}

type staffLoggedOut struct {
	StaffID     uuid.UUID `json:"staff_id"`
	Role        string    `json:"role"`
	SessionID   string    `json:"session_id"`
	LoggedOutAt time.Time `json:"logged_out_at"`
}

// NewStaffLoggedIn — staff.logged_in (shared/events/staff.logged_in.json). method: "email" | "sms". PII yo'q.
func NewStaffLoggedIn(staffID uuid.UUID, role, sessionID, method string, now time.Time, traceID string) (Event, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Event{}, fmt.Errorf("events: uuid: %w", err)
	}
	return build(id, "staff.logged_in", SubjectStaffLoggedIn, now, traceID, staffLoggedIn{
		StaffID: staffID, Role: role, SessionID: sessionID, Method: method, LoggedInAt: now.UTC(),
	})
}

// NewStaffLoggedOut — staff.logged_out (shared/events/staff.logged_out.json). event_id = UUIDv5(sessionID).
func NewStaffLoggedOut(staffID uuid.UUID, role, sessionID string, now time.Time, traceID string) (Event, error) {
	id := uuid.NewSHA1(staffLogoutNS, []byte("staff.logged_out:"+sessionID))
	return build(id, "staff.logged_out", SubjectStaffLoggedOut, now, traceID, staffLoggedOut{
		StaffID: staffID, Role: role, SessionID: sessionID, LoggedOutAt: now.UTC(),
	})
}

func build(id uuid.UUID, name, subject string, now time.Time, traceID string, payload any) (Event, error) {
	body, err := json.Marshal(envelope{Event: name, Version: 1, EventID: id, OccurredAt: now.UTC(), TraceID: traceID, Payload: payload})
	if err != nil {
		return Event{}, fmt.Errorf("events: marshal: %w", err)
	}
	return Event{ID: id, Subject: subject, Payload: body, CreatedAt: now.UTC()}, nil
}

// Publisher — hodisani brokerga yuboradi.
type Publisher interface {
	Publish(ctx context.Context, e Event) error
}

// Outbox — yuborilmagan hodisalarni oladi, publish muvaffaqiyatli bo'lsa belgilaydi.
type Outbox interface {
	DrainOutbox(ctx context.Context, limit int, publish func(context.Context, Event) error) (int, error)
}
