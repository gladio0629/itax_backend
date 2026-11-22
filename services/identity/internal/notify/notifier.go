package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
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
	SMSMode             string
	SMSProviderURL      string // Eskiz API manzili; bo'sh bo'lsa EskizBaseURL
	SMSProviderEmail    string
	SMSProviderPassword string
	// SMSTokens — Eskiz token keshi (main: Redis). nil bo'lsa jarayon ichidagi MemTokenStore.
	SMSTokens TokenStore

	EmailMode     string
	EmailProvider string
	EmailAPIKey   string
	EmailFrom     string

	// OTPTTL — email matnidagi "Kod N daqiqa amal qiladi" uchun.
	OTPTTL time.Duration
}

// Notifier — auth.Notifier: har bir kanal alohida real yoki mock (Fallback).
type Notifier struct {
	sms      *Eskiz  // nil => mock
	email    *Resend // nil => mock
	fallback Fallback
	ttl      time.Duration
	log      *slog.Logger
}

// UseReal — real rejim faqat mode=real VA barcha maxfiy qiymatlar (kalit / email+parol) bo'sh bo'lmaganda.
func UseReal(mode string, secrets ...string) bool {
	if !strings.EqualFold(strings.TrimSpace(mode), ModeReal) || len(secrets) == 0 {
		return false
	}
	for _, s := range secrets {
		if strings.TrimSpace(s) == "" {
			return false
		}
	}
	return true
}

// New — rejimni aniqlaydi. mode=real, lekin kalit/parol bo'sh bo'lsa: ogohlantirish va mock (servis to'xtamaydi).
func New(cfg Config, fallback Fallback, log *slog.Logger) *Notifier {
	n := &Notifier{fallback: fallback, ttl: cfg.OTPTTL, log: log}

	if UseReal(cfg.SMSMode, cfg.SMSProviderEmail, cfg.SMSProviderPassword) {
		tokens := cfg.SMSTokens
		if tokens == nil {
			tokens = &MemTokenStore{}
		}
		n.sms = &Eskiz{BaseURL: cfg.SMSProviderURL, Email: cfg.SMSProviderEmail, Password: cfg.SMSProviderPassword, Tokens: tokens}
		log.Info("SMS provayder: real (eskiz.uz)")
	} else {
		if strings.EqualFold(strings.TrimSpace(cfg.SMSMode), ModeReal) {
			log.Warn("SMS_MODE=real, lekin SMS_PROVIDER_EMAIL yoki SMS_PROVIDER_PASSWORD bo'sh: SMS mock rejimda (logga yoziladi)")
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
	res, err := n.sms.Send(ctx, phone, SMSText(code))
	if err != nil {
		n.log.WarnContext(ctx, "SMS yuborilmadi", "phone", MaskPhone(phone), "err", err)
		return err
	}
	n.log.InfoContext(ctx, "SMS yuborildi", "phone", MaskPhone(phone), "provider_id", res.ID, "status", res.Status)
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

// MaskPhone — "+998901234567" -> "+998*****67".
func MaskPhone(p string) string {
	if len(p) < 8 {
		return "***"
	}
	return p[:4] + "*****" + p[len(p)-2:]
}

// MaskEmail — "admin@example.com" -> "a***@example.com".
func MaskEmail(e string) string {
	at := strings.LastIndexByte(e, '@')
	if at < 1 {
		return "***"
	}
	return e[:1] + "***" + e[at:]
}
