// Package sms — SMS shablonlari va yuboruvchilar: Mock (log) va Xabarnoma (smsxabarnoma.uz, SMS_MODE=real).
package sms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

var ErrUnknownTemplate = errors.New("sms: unknown template")

// Template — kalit bo'yicha matn; {param} o'rinlari params bilan almashtiriladi, Required params majburiy.
type Template struct {
	Text     string
	Required []string
}

// Templates — SMS matnlari (uz). Yangi shablon shu yerga qo'shiladi.
var Templates = map[string]Template{
	"otp":         {Text: "IZGO: tasdiqlash kodi {code}. Hech kimga bermang.", Required: []string{"code"}},
	"admin_login": {Text: "IZGO admin: kirish kodi {code}.", Required: []string{"code"}},
}

// Render — shablonni to'ldiradi. Noma'lum shablon: ErrUnknownTemplate; yetishmagan param: xato (nom bilan).
func Render(key string, params map[string]string) (string, error) {
	t, ok := Templates[key]
	if !ok {
		return "", ErrUnknownTemplate
	}
	for _, r := range t.Required {
		if strings.TrimSpace(params[r]) == "" {
			return "", fmt.Errorf("sms: param %q kerak", r)
		}
	}
	out := t.Text
	for k, v := range params {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return out, nil
}

type Sender interface {
	Send(ctx context.Context, phone, text string) error
}

// Mock — haqiqiy provayder o'rniga log. Matn (OTP kodi bor) faqat LogText=true bo'lganda yoziladi (dev).
type Mock struct {
	Log     *slog.Logger
	LogText bool
}

func (m Mock) Send(ctx context.Context, phone, text string) error {
	args := []any{"phone", MaskPhone(phone)}
	if m.LogText {
		args = append(args, "text", text)
	}
	m.Log.InfoContext(ctx, "SMS (mock, haqiqiy yuborilmaydi)", args...)
	return nil
}

func MaskPhone(p string) string {
	if len(p) < 8 {
		return "***"
	}
	return p[:4] + "*****" + p[len(p)-2:]
}
