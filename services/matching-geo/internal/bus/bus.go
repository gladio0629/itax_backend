// Package bus — NATS JetStream: offer.created yuborish va order.accepted / trip.completed tinglash.
package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/izgo/matching-geo/internal/events"
	"github.com/nats-io/nats.go"
)

const streamName = "MATCHING_EVENTS"

type Conn struct {
	nc  *nats.Conn
	js  nats.JetStreamContext
	log *slog.Logger

	mu    sync.Mutex
	ready bool
}

// Connect — NATS broker hali ko'tarilmagan bo'lsa ham servis ishga tushadi (ulanish fonda qayta uriniladi).
func Connect(url string, log *slog.Logger) (*Conn, error) {
	opts := []nats.Option{
		nats.Name("izgo-matching-geo"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Warn("nats uzildi", "err", err)
			}
		}),
		nats.ReconnectHandler(func(c *nats.Conn) { log.Info("nats qayta ulandi", "url", c.ConnectedUrl()) }),
	}
	// F-01: har servisning o'z foydalanuvchisi (faqat o'z hodisalarini chiqaradi) va TLS. Bo'sh bo'lsa — eski xulq (testlar).
	if u := os.Getenv("NATS_USER"); u != "" {
		opts = append(opts, nats.UserInfo(u, os.Getenv("NATS_PASSWORD")))
	}
	if ca := os.Getenv("NATS_CA_FILE"); ca != "" {
		opts = append(opts, nats.RootCAs(ca))
	}
	nc, err := nats.Connect(url, opts...)
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

// ensureStream — offer.created uchun stream. Boshqa servis shu subject'ni qamrab oluvchi stream yaratgan bo'lsa
// (overlap) xato yutiladi: publish baribir ishlaydi.
func (c *Conn) ensureStream(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready {
		return nil
	}
	_, err := c.js.StreamInfo(streamName, nats.Context(ctx))
	if errors.Is(err, nats.ErrStreamNotFound) {
		if _, err = c.js.AddStream(&nats.StreamConfig{
			Name: streamName, Subjects: []string{"izgo.offer.>"}, Storage: nats.FileStorage, Retention: nats.LimitsPolicy,
			MaxAge: 7 * 24 * time.Hour, Duplicates: 10 * time.Minute,
		}, nats.Context(ctx)); err != nil {
			c.log.Warn("stream yaratib bo'lmadi, mavjud streamga tayanamiz", "stream", streamName, "err", err)
			err = nil
		}
	}
	if err != nil {
		return fmt.Errorf("bus: stream: %w", err)
	}
	c.ready = true
	return nil
}

// PublishOfferCreated — Nats-Msg-Id = offer_id (takroriy chaqiruvda JetStream dedupe qiladi).
func (c *Conn) PublishOfferCreated(ctx context.Context, o events.OfferCreated, trace string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.ensureStream(ctx); err != nil {
		return err
	}
	body, err := events.BuildOfferCreated(o, time.Now(), trace)
	if err != nil {
		return err
	}
	if _, err := c.js.Publish(events.SubjectOfferCreated, body, nats.MsgId(o.OfferID.String()), nats.Context(ctx)); err != nil {
		return fmt.Errorf("bus: publish %s: %w", events.SubjectOfferCreated, err)
	}
	return nil
}

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

// Run — har bir obuna uchun durable consumer yaratadi. Stream hali yo'q bo'lsa (Trip & Order birinchi hodisani
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
		// DeliverAll: servis o'chiq turganda yoki yangi o'rnatilganda busy/available holatlari hodisalardan qayta tiklanadi
		// (ishlovchilar takroriy/tartibsiz hodisalarga chidamli).
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
