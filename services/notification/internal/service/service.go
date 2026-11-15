// Package service — hodisa -> bildirishnoma: ro'yxatga yozish, real-time (WebSocket) yoki push yetkazish.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/notification/internal/events"
	"github.com/izgo/notification/internal/push"
	"github.com/izgo/notification/internal/store"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	st     *store.Store
	rdb    redis.Cmdable
	pusher push.Pusher
	log    *slog.Logger
	now    func() time.Time
}

func New(st *store.Store, rdb redis.Cmdable, p push.Pusher, log *slog.Logger) *Service {
	return &Service{st: st, rdb: rdb, pusher: p, log: log, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// Handle — bitta NATS hodisasi. events.ErrPoison -> tashlanadi; events.ErrNoContext va boshqa xatolar -> keyinroq qayta uriniladi.
// Takroriy yetkazish xavfsiz: bildirishnoma ID si (hodisa, foydalanuvchi) bo'yicha deterministik va faqat bir marta yoziladi.
func (s *Service) Handle(ctx context.Context, raw []byte) error {
	n, err := events.Parse(raw)
	if err != nil {
		return err
	}
	if n.RememberOrder != uuid.Nil {
		if err := s.st.RememberOrder(ctx, n.RememberOrder, n.Recipient); err != nil {
			return err
		}
	}
	user := n.Recipient
	if user == uuid.Nil {
		p, ok, err := s.st.PassengerOf(ctx, n.LookupOrder)
		if err != nil {
			return err
		}
		if !ok {
			// order.accepted order.created dan oldin kelishi mumkin (alohida consumer'lar): keyinroq qayta uriniladi.
			return fmt.Errorf("%w: order %s", events.ErrNoContext, n.LookupOrder)
		}
		user = p
	}

	created, err := s.st.Add(ctx, user, store.Notification{
		ID: store.NotificationID(n.EventID, user), Kind: n.Kind, TitleKey: n.TitleKey(), BodyKey: n.BodyKey(),
		Params: n.Params(), CreatedAt: s.now(),
	})
	if err != nil {
		return err
	}
	if !created {
		return nil // allaqachon yetkazilgan
	}
	s.deliver(ctx, user, n)
	return nil
}

// deliver — foydalanuvchining ochiq WebSocket ulanishlariga (barcha replikalarda) Redis pub/sub orqali; hech kim
// qabul qilmasa (ulanish yo'q) — push.
func (s *Service) deliver(ctx context.Context, user uuid.UUID, n events.Notice) {
	body, _ := json.Marshal(n.WSMessage())
	receivers, err := s.rdb.Publish(ctx, store.WSChannel(user), body).Result()
	if err != nil {
		s.log.WarnContext(ctx, "real-time yuborib bo'lmadi: push'ga o'tiladi", "user_id", user, "err", err)
	}
	if err == nil && receivers > 0 {
		return
	}
	s.pushTo(ctx, user, n.PushData())
}

func (s *Service) pushTo(ctx context.Context, user uuid.UUID, data map[string]string) {
	devices, err := s.st.Devices(ctx, user)
	if err != nil {
		s.log.ErrorContext(ctx, "qurilmalarni o'qib bo'lmadi", "user_id", user, "err", err)
		return
	}
	if len(devices) == 0 {
		s.log.InfoContext(ctx, "push: qurilma yo'q", "user_id", user, "type", data["type"])
		return
	}
	for _, d := range devices {
		switch err := s.pusher.Send(ctx, d.Platform, d.Token, data); {
		case errors.Is(err, push.ErrUnregistered):
			_ = s.st.RemoveDevice(ctx, user, d.Token)
		case err != nil:
			s.log.WarnContext(ctx, "push yuborilmadi", "user_id", user, "platform", d.Platform, "token", push.Mask(d.Token), "err", err)
		}
	}
}
