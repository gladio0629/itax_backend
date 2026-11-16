package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/izgo/shared/go/pii"
)

// Rejimlar (SMS_MODE / EMAIL_MODE).
const (
	ModeMock = "mock"
	ModeReal = "real"
)

// Fallback — mock yo'l (hozirgi auth.LogNotifier): kod logga yoziladi.
type Fallback interface {
	SendSMS(ctx context.Context, phone, code string) error
	SendEmail(ctx context.Context, email, code string) error
}

// Config — main.go config.Config dan to'ldiradi.
type Config struct {
	SMSMode        string
	SMSProviderURL string
	SMSProviderKey string

	EmailMode     string
	EmailProvider string
	EmailAPIKey   string
	EmailFrom     string

	// OTPTTL — email matnidagi "Kod N daqiqa amal qiladi" uchun.
	OTPTTL time.Duration
}

// Notifier — auth.Notifier: har bir kanal alohida real yoki mock (Fallback).
type Notifier struct {
	sms      *SMSXabarnoma // nil => mock
	email    *Resend       // nil => mock
	fallback Fallback
	ttl      time.Duration
	log      *slog.Logger
	now      func() time.Time
}

// UseReal — real rejim faqat mode=real VA kalit bo'sh bo'lmaganda.
func UseReal(mode, key string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), ModeReal) && strings.TrimSpace(key) != ""
}

// New — rejimni aniqlaydi. mode=real, lekin kalit bo'sh bo'lsa: ogohlantirish va mock (servis to'xtamaydi).
func New(cfg Config, fallback Fallback, log *slog.Logger) *Notifier {
	n := &Notifier{fallback: fallback, ttl: cfg.OTPTTL, log: log, now: time.Now}

	if UseReal(cfg.SMSMode, cfg.SMSProviderKey) {
		n.sms = &SMSXabarnoma{BaseURL: cfg.SMSProviderURL, APIKey: cfg.SMSProviderKey}
		log.Info("SMS provayder: real (smsxabarnoma.uz)")
	} else {
		if strings.EqualFold(strings.TrimSpace(cfg.SMSMode), ModeReal) {
			log.Warn("SMS_MODE=real, lekin SMS_PROVIDER_KEY bo'sh: SMS mock rejimda (logga yoziladi)")
		}
		log.Info("SMS provayder: mock")
	}

	if UseReal(cfg.EmailMode, cfg.EmailAPIKey) {
		n.email = &Resend{APIKey: cfg.EmailAPIKey, From: cfg.EmailFrom}
		log.Info("Email provayder: real", "provider", cfg.EmailProvider)
	} else {
		if strings.EqualFold(strings.TrimSpace(cfg.EmailMode), ModeReal) {
			log.Warn("EMAIL_MODE=real, lekin EMAIL_API_KEY bo'sh: email mock rejimda (logga yoziladi)")
		}
		log.Info("Email provayder: mock")
	}
	return n
}

// SMSReal / EmailReal — qaysi yo'l tanlangani (test va diagnostika uchun).
func (n *Notifier) SMSReal() bool   { return n.sms != nil }
func (n *Notifier) EmailReal() bool { return n.email != nil }

// SMSText — OTP SMS matni.
func SMSText(code string) string { return "IZGO tasdiqlash kodi: " + code }

// EmailSubject / EmailText — admin kirish kodi xati.
const EmailSubject = "IZGO Admin - kirish kodi"

func EmailText(code string, ttl time.Duration) string {
	mins := int(ttl.Round(time.Minute) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	return fmt.Sprintf("Sizning kodingiz: %s\nKod %d daqiqa amal qiladi.", code, mins)
}

func (n *Notifier) SendSMS(ctx context.Context, phone, code string) error {
	if n.sms == nil {
		return n.fallback.SendSMS(ctx, phone, code)
	}
	id := ExternalID("otp", Ref(phone), n.now())
	res, err := n.sms.Send(ctx, phone, SMSText(code), id)
	if err != nil {
		n.log.WarnContext(ctx, "SMS yuborilmadi", "phone", MaskPhone(phone), "external_id", id, "err", err)
		return err
	}
	n.log.InfoContext(ctx, "SMS yuborildi", "phone", MaskPhone(phone), "external_id", id,
		"provider_id", res.ID, "status", res.Status, "parts", res.Parts)
	return nil
}

func (n *Notifier) SendEmail(ctx context.Context, email, code string) error {
	if n.email == nil {
		return n.fallback.SendEmail(ctx, email, code)
	}
	id, err := n.email.Send(ctx, email, EmailSubject, EmailText(code, n.ttl))
	if err != nil {
		n.log.WarnContext(ctx, "Email yuborilmadi", "email", MaskEmail(email), "err", err)
		return err
	}
	n.log.InfoContext(ctx, "Email yuborildi", "email", MaskEmail(email), "provider_id", id)
	return nil
}

// MaskPhone / MaskEmail — logdagi ko'rinish (shared/go/pii): "998*******67", "a***@***.com".
func MaskPhone(p string) string { return pii.MaskPhone(p) }

func MaskEmail(e string) string { return pii.MaskEmail(e) }
