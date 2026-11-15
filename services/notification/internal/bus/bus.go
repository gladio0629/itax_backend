// Package bus — NATS JetStream: hodisalarni TINGLASH (hech narsa chiqarmaydi).
package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/izgo/notification/internal/events"
	"github.com/nats-io/nats.go"
)

type Conn struct {
	nc  *nats.Conn
	js  nats.JetStreamContext
	log *slog.Logger
}

// Connect — NATS broker hali ko'tarilmagan bo'lsa ham servis ishga tushadi (ulanish fonda qayta uriniladi).
func Connect(url string, log *slog.Logger) (*Conn, error) {
	nc, err := nats.Connect(url,
		nats.Name("izgo-notification"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Warn("nats uzildi", "err", err)
			}
		}),
		nats.ReconnectHandler(func(c *nats.Conn) { log.Info("nats qayta ulandi", "url", c.ConnectedUrl()) }),
	)
	if err != nil {
		return nil, fmt.Errorf("bus: nats ulanish: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("bus: jetstream: %w", err)
	}
	return &Conn{nc: nc, js: js, log: log}, nil
}

func (c *Conn) Close() { _ = c.nc.Drain() }

// Handler — hodisani qayta ishlaydi. events.ErrPoison -> xabar tashlanadi (Term); boshqa xato -> keyinroq qayta uriniladi.
type Handler func(ctx context.Context, raw []byte) error

type Sub struct {
	Subject string
	Durable string
	Handle  Handler
}

const (
	ackWait    = 30 * time.Second
	maxDeliver = 10
	nakDelay   = 5 * time.Second
	retryEvery = 5 * time.Second
)

// Run — har bir obuna uchun durable consumer yaratadi. Stream hali yo'q bo'lsa (hodisa egasi birinchi hodisani
// chiqarmaguncha yaratilmaydi) har 5 soniyada qayta uriniladi. ctx bekor bo'lguncha ishlaydi.
func (c *Conn) Run(ctx context.Context, subs ...Sub) {
	var wg sync.WaitGroup
	for _, s := range subs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.runOne(ctx, s)
		}()
	}
	wg.Wait()
}

func (c *Conn) runOne(ctx context.Context, s Sub) {
	for {
		// DeliverAll: servis o'chiq turganda kelgan hodisalar ham yetkaziladi (ishlovchi takroriy yetkazishga chidamli).
		sub, err := c.js.Subscribe(s.Subject, func(m *nats.Msg) { c.dispatch(ctx, s, m) },
			nats.Durable(s.Durable), nats.ManualAck(), nats.AckWait(ackWait), nats.MaxDeliver(maxDeliver), nats.DeliverAll())
		if err == nil {
			c.log.Info("hodisalarga obuna bo'lindi", "subject", s.Subject, "durable", s.Durable)
			<-ctx.Done()
			_ = sub.Drain()
			return
		}
		c.log.Warn("obuna bo'lib bo'lmadi (qayta uriniladi)", "subject", s.Subject, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryEvery):
		}
	}
}

func (c *Conn) dispatch(ctx context.Context, s Sub, m *nats.Msg) {
	err := s.Handle(ctx, m.Data)
	switch {
	case err == nil:
		_ = m.Ack()
	case errors.Is(err, events.ErrPoison):
		c.log.Error("buzuq hodisa tashlandi", "subject", s.Subject, "err", err)
		_ = m.Term()
	default:
		c.log.Warn("hodisani qayta ishlab bo'lmadi (qayta uriniladi)", "subject", s.Subject, "err", err)
		_ = m.NakWithDelay(nakDelay)
	}
}
