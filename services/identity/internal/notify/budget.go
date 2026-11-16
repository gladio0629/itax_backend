package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// tashkent — kunlik byudjet chegarasi Toshkent vaqti (UTC+5, yozgi vaqt yo'q). Qat'iy siljish: distroless'da tzdata shart emas.
var tashkent = time.FixedZone("Asia/Tashkent", 5*60*60)

// SMSBudget — F-04 SMS byudjet signali. Barcha OTP SMS'lari (yo'lovchi/haydovchi, admin) shu dekorator orqali o'tadi:
// muvaffaqiyatli yuborilgan har bir SMS Redis'dagi kunlik hisoblagichni oshiradi; Limit dan oshsa logga ERROR "ALERT".
// Faqat signal: yuborish to'xtatilmaydi. Hisoblagich xatosi yuborishni buzmaydi (faqat ogohlantirish).
// Signal limitdan oshgan birinchi SMS'da, keyin har Limit/10 tadan (kamida 1) bir marta yoziladi — log toshqini bo'lmasin.
type SMSBudget struct {
	Next  Fallback // haqiqiy yuboruvchi (Notifier)
	RDB   redis.Cmdable
	Limit int // 0 — o'chirilgan
	Log   *slog.Logger
	Now   func() time.Time
}

func budgetKey(day string) string { return "sms:budget:" + day }

func (b *SMSBudget) SendSMS(ctx context.Context, phone, code string) error {
	if err := b.Next.SendSMS(ctx, phone, code); err != nil {
		return err
	}
	if b.Limit > 0 {
		b.count(ctx)
	}
	return nil
}

func (b *SMSBudget) SendEmail(ctx context.Context, email, code string) error {
	return b.Next.SendEmail(ctx, email, code)
}

func (b *SMSBudget) count(ctx context.Context) {
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	day := now().In(tashkent).Format(time.DateOnly)
	n, err := b.incr(ctx, budgetKey(day))
	if err != nil {
		b.Log.WarnContext(ctx, "SMS byudjet hisoblagichi ishlamadi", "err", err)
		return
	}
	over := n - int64(b.Limit)
	if over <= 0 {
		return
	}
	step := int64(max(b.Limit/10, 1))
	if over == 1 || over%step == 0 {
		b.Log.ErrorContext(ctx, "ALERT F-04: SMS kunlik byudjeti oshdi", "alert", "sms_daily_budget",
			"day", day, "count", n, "limit", b.Limit)
	}
}

func (b *SMSBudget) incr(ctx context.Context, key string) (int64, error) {
	pipe := b.RDB.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, 48*time.Hour) // kun kaliti; ertasi kuni ham ko'rish uchun 2 kun saqlanadi
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("sms byudjet: %w", err)
	}
	return incr.Val(), nil
}
