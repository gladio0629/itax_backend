package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const streamName = "USER_EVENTS"

// NATSPublisher — JetStream orqali yuboradi.
type NATSPublisher struct {
	nc  *nats.Conn
	js  nats.JetStreamContext
	log *slog.Logger

	mu    sync.Mutex
	ready bool
}

// Connect — NATS'ga ulanadi. Broker hali ko'tarilmagan bo'lsa ham servis ishga tushadi:
// ulanish fonda qayta uriniladi, hodisalar outbox'da kutadi.
func Connect(url string, log *slog.Logger) (*NATSPublisher, error) {
	nc, err := nats.Connect(url,
		nats.Name("izgo-identity"),
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
		return nil, fmt.Errorf("events: nats ulanish: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("events: jetstream: %w", err)
	}
	return &NATSPublisher{nc: nc, js: js, log: log}, nil
}

func (p *NATSPublisher) Close() {
	_ = p.nc.Drain()
}

// ensureStream — shared/events uchun stream bo'lmasa yaratadi. Boshqa servis shu subject'ni
// qamrab oluvchi stream yaratgan bo'lsa (overlap), xato yutiladi: publish baribir ishlaydi.
func (p *NATSPublisher) ensureStream(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ready {
		return nil
	}
	_, err := p.js.StreamInfo(streamName, nats.Context(ctx))
	if errors.Is(err, nats.ErrStreamNotFound) {
		_, err = p.js.AddStream(&nats.StreamConfig{
			Name:       streamName,
			Subjects:   []string{"izgo.user.>"},
			Storage:    nats.FileStorage,
			Retention:  nats.LimitsPolicy,
			MaxAge:     7 * 24 * time.Hour,
			Duplicates: 10 * time.Minute,
		}, nats.Context(ctx))
		if err != nil {
			p.log.Warn("stream yaratib bo'lmadi, mavjud streamga tayanamiz", "stream", streamName, "err", err)
			err = nil
		}
	}
	if err != nil {
		return fmt.Errorf("events: stream: %w", err)
	}
	p.ready = true
	return nil
}

func (p *NATSPublisher) Publish(ctx context.Context, e Event) error {
	if err := p.ensureStream(ctx); err != nil {
		return err
	}
	if _, err := p.js.Publish(e.Subject, e.Payload, nats.MsgId(e.ID.String()), nats.Context(ctx)); err != nil {
		return fmt.Errorf("events: publish %s: %w", e.Subject, err)
	}
	return nil
}

// Relay — outbox'ni davriy (va Kick bilan darhol) NATS'ga yuboradi.
type Relay struct {
	outbox   Outbox
	pub      Publisher
	log      *slog.Logger
	interval time.Duration
	batch    int
	kick     chan struct{}
}

func NewRelay(outbox Outbox, pub Publisher, log *slog.Logger) *Relay {
	return &Relay{outbox: outbox, pub: pub, log: log, interval: 2 * time.Second, batch: 50, kick: make(chan struct{}, 1)}
}

// Kick — keyingi tikni kutmasdan yuborishni boshlaydi.
func (r *Relay) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Run — ctx bekor bo'lguncha ishlaydi.
func (r *Relay) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		r.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.kick:
		}
	}
}

func (r *Relay) drain(ctx context.Context) {
	for ctx.Err() == nil {
		pubCtx := func(c context.Context, e Event) error {
			c, cancel := context.WithTimeout(c, 5*time.Second)
			defer cancel()
			return r.pub.Publish(c, e)
		}
		n, err := r.outbox.DrainOutbox(ctx, r.batch, pubCtx)
		if err != nil {
			if ctx.Err() == nil {
				r.log.Warn("outbox yuborilmadi (qayta uriniladi)", "err", err)
			}
			return
		}
		if n < r.batch {
			return
		}
	}
}
