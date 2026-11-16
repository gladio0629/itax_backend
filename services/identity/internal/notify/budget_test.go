package notify

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type stubSender struct {
	mu   sync.Mutex
	sms  int
	fail bool
}

func (s *stubSender) SendSMS(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return errors.New("provayder ishlamadi")
	}
	s.sms++
	return nil
}

func (s *stubSender) SendEmail(context.Context, string, string) error { return nil }

func alerts(buf *bytes.Buffer) int { return strings.Count(buf.String(), `"alert":"sms_daily_budget"`) }

func TestSMSBudgetAlert(t *testing.T) {
	cases := []struct {
		name       string
		limit      int
		sends      int
		wantAlerts int
	}{
		{"limit ichida", 1000, 1000, 0},
		{"1001-SMS — signal", 1000, 1001, 1},
		{"keyin har 100 tada bir marta", 1000, 1250, 3}, // 1001, 1100, 1200
		{"kichik limit: har biri", 3, 6, 3},
		{"o'chirilgan", 0, 50, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			var buf bytes.Buffer
			next := &stubSender{}
			b := &SMSBudget{Next: next, RDB: rdb, Limit: c.limit, Log: slog.New(slog.NewJSONHandler(&buf, nil))}
			for i := 0; i < c.sends; i++ {
				if err := b.SendSMS(t.Context(), "+998901234567", "123456"); err != nil {
					t.Fatal(err)
				}
			}
			if next.sms != c.sends {
				t.Fatalf("yuborilgan %d, kutilgan %d (signal yuborishni to'xtatmasligi kerak)", next.sms, c.sends)
			}
			if got := alerts(&buf); got != c.wantAlerts {
				t.Fatalf("ALERT soni %d, kutilgan %d\n%s", got, c.wantAlerts, buf.String())
			}
			if c.wantAlerts > 0 && !strings.Contains(buf.String(), `"level":"ERROR"`) {
				t.Fatalf("ALERT ERROR darajasida bo'lishi kerak: %s", buf.String())
			}
		})
	}
}

func TestSMSBudgetDayRolloverAndFailures(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	var buf bytes.Buffer
	next := &stubSender{}
	// 2026-11-16 18:59 UTC = Toshkentda 23:59
	now := time.Date(2026, 11, 16, 18, 59, 0, 0, time.UTC)
	b := &SMSBudget{Next: next, RDB: rdb, Limit: 2, Log: slog.New(slog.NewJSONHandler(&buf, nil)), Now: func() time.Time { return now }}
	ctx := t.Context()

	for range 2 {
		_ = b.SendSMS(ctx, "+998901234567", "1")
	}
	if v, _ := mr.Get("sms:budget:2026-11-16"); v != "2" {
		t.Fatalf("kun hisoblagichi = %q", v)
	}
	if ttl := mr.TTL("sms:budget:2026-11-16"); ttl <= 0 || ttl > 48*time.Hour {
		t.Fatalf("TTL = %v", ttl)
	}
	// Toshkentda yangi kun (19:00 UTC = 00:00) — hisob qaytadan
	now = now.Add(time.Minute)
	_ = b.SendSMS(ctx, "+998901234567", "1")
	if v, _ := mr.Get("sms:budget:2026-11-17"); v != "1" || alerts(&buf) != 0 {
		t.Fatalf("yangi kun: %q, alerts=%d", v, alerts(&buf))
	}

	// yuborilmagan SMS hisoblanmaydi va xato qaytadi
	next.fail = true
	if err := b.SendSMS(ctx, "+998901234567", "1"); err == nil {
		t.Fatal("provayder xatosi qaytishi kerak")
	}
	if v, _ := mr.Get("sms:budget:2026-11-17"); v != "1" {
		t.Fatalf("muvaffaqiyatsiz SMS hisoblandi: %q", v)
	}

	// Redis ishlamasa — SMS baribir yuboriladi (faqat ogohlantirish)
	next.fail = false
	mr.Close()
	if err := b.SendSMS(ctx, "+998901234567", "1"); err != nil {
		t.Fatalf("hisoblagich xatosi yuborishni buzmasligi kerak: %v", err)
	}
	if !strings.Contains(buf.String(), "SMS byudjet hisoblagichi ishlamadi") {
		t.Fatalf("ogohlantirish yo'q: %s", buf.String())
	}
}
