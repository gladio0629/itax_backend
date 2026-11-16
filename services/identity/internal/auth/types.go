// Package auth — identity servisining biznes logikasi (HTTP va saqlash qatlamidan mustaqil).
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/identity/internal/events"
	"github.com/izgo/identity/internal/token"
	"github.com/izgo/shared/roles"
)

// ErrNotFound — yozuv topilmadi.
var ErrNotFound = errors.New("auth: not found")

// CreateStaff unikal cheklov xatolari (repo qaytaradi, servis apperr ga aylantiradi).
var (
	ErrStaffEmailTaken = errors.New("auth: staff email taken")
	ErrStaffPhoneTaken = errors.New("auth: staff phone taken")
)

// App — so'rov qaysi ilovadan kelayotgani (X-App sarlavhasi). OpenAPI so'rov sxemalarida
// bunday maydon yo'q, shuning uchun ilova turi sarlavhadan olinadi; bo'lmasa "client".
type App string

const (
	AppClient App = "client"
	AppDriver App = "driver"
)

func ParseApp(h string) (App, bool) {
	switch App(h) {
	case "", AppClient:
		return AppClient, true
	case AppDriver:
		return AppDriver, true
	}
	return "", false
}

func (a App) Role() roles.Role {
	if a == AppDriver {
		return roles.Driver
	}
	return roles.Passenger
}

func (a App) Audience() token.Audience {
	if a == AppDriver {
		return token.AudDriver
	}
	return token.AudClient
}

func audienceForRole(r roles.Role) token.Audience {
	if r == roles.Driver {
		return token.AudDriver
	}
	return token.AudClient
}

type Notifications struct {
	Push  bool `json:"push"`
	SMS   bool `json:"sms"`
	Promo bool `json:"promo"`
}

type Settings struct {
	Language      string        `json:"language"`
	Theme         string        `json:"theme"`
	Notifications Notifications `json:"notifications"`
}

func DefaultSettings() Settings {
	return Settings{Language: "uz", Theme: "system", Notifications: Notifications{Push: true, SMS: true, Promo: true}}
}

// SettingsUpdate — qisman yangilash (faqat berilgan maydonlar o'zgaradi).
type SettingsUpdate struct {
	Language      *string
	Theme         *string
	Notifications *NotificationsUpdate
}

type NotificationsUpdate struct{ Push, SMS, Promo *bool }

func (s Settings) Apply(u SettingsUpdate) Settings {
	if u.Language != nil {
		s.Language = *u.Language
	}
	if u.Theme != nil {
		s.Theme = *u.Theme
	}
	if n := u.Notifications; n != nil {
		if n.Push != nil {
			s.Notifications.Push = *n.Push
		}
		if n.SMS != nil {
			s.Notifications.SMS = *n.SMS
		}
		if n.Promo != nil {
			s.Notifications.Promo = *n.Promo
		}
	}
	return s
}

type User struct {
	ID        uuid.UUID
	Phone     string
	Role      roles.Role
	FirstName *string
	LastName  *string
	Settings  Settings
	Rating    *float64
	CreatedAt time.Time
}

type Staff struct {
	ID uuid.UUID
	// Email — nil faqat operator uchun (u telefon + SMS bilan kiradi, email qadamisiz).
	Email    *string
	Phone    string
	FullName string
	Role     roles.Role
	Active   bool
}

type RefreshToken struct {
	JTI       uuid.UUID
	UserID    uuid.UUID
	Family    uuid.UUID
	ExpiresAt time.Time
}

type RotateOutcome int

const (
	Rotated  RotateOutcome = iota // eski token endi ishlatilgan, yangisi yozildi
	Reused                        // eski token allaqachon ishlatilgan (o'g'irlanish alomati)
	Rejected                      // token yo'q, muddati tugagan yoki bekor qilingan
)

// Repo — saqlash qatlami (PostgreSQL). Testlarda xotiradagi implementatsiya bor.
type Repo interface {
	// FindOrCreateUser: (phone, role) bo'yicha topadi yoki cand bilan yaratadi. Yaratilsa,
	// newEvent chaqirilib, hodisa foydalanuvchi bilan bir tranzaksiyada outbox'ga yoziladi.
	FindOrCreateUser(ctx context.Context, cand User, newEvent func(User) (events.Event, error)) (User, bool, error)
	GetUser(ctx context.Context, id uuid.UUID) (User, error)
	UpdateUserName(ctx context.Context, id uuid.UUID, first, last *string) (User, error)
	UpdateUserSettings(ctx context.Context, id uuid.UUID, s Settings) error

	InsertRefresh(ctx context.Context, t RefreshToken) error
	// RotateRefresh: old'ni atomik "ishlatilgan" qiladi va next'ni yozadi (bitta tranzaksiya).
	// Reused bo'lsa, foydalanuvchi ID si qaytadi.
	RotateRefresh(ctx context.Context, old uuid.UUID, next RefreshToken) (RotateOutcome, uuid.UUID, error)
	RevokeFamily(ctx context.Context, family uuid.UUID) error
	// RevokeAllForUser: foydalanuvchining barcha refresh tokenlarini bekor qiladi va
	// faol oilalar (sid) ro'yxatini qaytaradi.
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)

	GetStaffByEmail(ctx context.Context, email string) (Staff, error)
	GetStaffByPhone(ctx context.Context, phone string) (Staff, error)
	GetStaff(ctx context.Context, id uuid.UUID) (Staff, error)
	TouchStaffLogin(ctx context.Context, id uuid.UUID, at time.Time) error
	// InsertEvent — hodisani outbox'ga yozadi (Relay NATS'ga yuboradi). Shu ID allaqachon bo'lsa — jim o'tadi
	// (deterministik ID li takroriy hodisa, masalan idempotent logout).
	InsertEvent(ctx context.Context, e events.Event) error
	// EnsureStaff — idempotent: email yoki telefon band bo'lsa hech narsa qilmaydi (bootstrap uchun).
	EnsureStaff(ctx context.Context, s Staff) error
	// CreateStaff — yangi xodim; email/telefon band bo'lsa ErrStaffEmailTaken / ErrStaffPhoneTaken.
	CreateStaff(ctx context.Context, s Staff) error
	// UpdateStaff — xodimni qulflab (bitta tranzaksiya) apply bilan o'zgartiradi va saqlaydi. apply xato qaytarsa
	// hech narsa yozilmaydi. Yo'q bo'lsa ErrNotFound; email/telefon band bo'lsa ErrStaffEmailTaken / ErrStaffPhoneTaken.
	UpdateStaff(ctx context.Context, id uuid.UUID, apply func(*Staff) error) (Staff, error)
}

// Notifier — kodlarni yetkazadi. Hozircha faqat log (real SMS/email yo'q).
type Notifier interface {
	SendSMS(ctx context.Context, phone, code string) error
	SendEmail(ctx context.Context, email, code string) error
}
