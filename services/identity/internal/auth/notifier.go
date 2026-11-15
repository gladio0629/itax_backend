package auth

import (
	"context"
	"log/slog"
)

// LogNotifier — kodni logga yozadi (faqat dev/mock: real SMS provayder yo'q).
// SMS_MODE/EMAIL_MODE=real bo'lsa notify.Notifier provayderga yuboradi; aks holda shu mock ishlatiladi.
type LogNotifier struct{ Log *slog.Logger }

func (n LogNotifier) SendSMS(ctx context.Context, phone, code string) error {
	n.Log.InfoContext(ctx, "SMS kod (mock, real SMS yuborilmaydi)", "phone", phone, "code", code)
	return nil
}

func (n LogNotifier) SendEmail(ctx context.Context, email, code string) error {
	n.Log.InfoContext(ctx, "Email kod (mock, real email yuborilmaydi)", "email", email, "code", code)
	return nil
}
