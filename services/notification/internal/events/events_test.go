package events

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func ev(name string, payload map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"event": name, "version": 1, "event_id": uuid.NewString(), "occurred_at": "2026-11-12T10:00:00Z", "payload": payload})
	return b
}

func TestParseRecipientsAndMinimalContent(t *testing.T) {
	p, d, o, tr, w := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	s := func(u uuid.UUID) string { return u.String() }
	cases := []struct {
		name      string
		raw       []byte
		recipient uuid.UUID
		lookup    uuid.UUID
		id        uuid.UUID
		status    string
	}{
		{"order.created -> yo'lovchi", ev("order.created", map[string]any{"order_id": s(o), "passenger_id": s(p), "price": 123, "from": map[string]any{"lat": 1}}), p, uuid.Nil, o, "searching"},
		{"offer.created -> haydovchi", ev("offer.created", map[string]any{"offer_id": s(tr), "order_id": s(o), "driver_id": s(d)}), d, uuid.Nil, tr, ""},
		{"order.accepted -> yo'lovchi (order bo'yicha)", ev("order.accepted", map[string]any{"order_id": s(o), "trip_id": s(tr), "driver_id": s(d), "offer_id": s(w)}), uuid.Nil, o, o, "accepted"},
		{"trip.completed -> yo'lovchi", ev("trip.completed", map[string]any{"trip_id": s(tr), "order_id": s(o), "driver_id": s(d), "passenger_id": s(p), "final_price": 5}), p, uuid.Nil, tr, "completed"},
		{"wallet.limit_reached -> haydovchi", ev("wallet.limit_reached", map[string]any{"wallet_id": s(w), "driver_id": s(d), "balance": -1}), d, uuid.Nil, w, ""},
		{"driver.approved -> haydovchi", ev("driver.approved", map[string]any{"driver_id": s(d), "approved_by": s(p)}), d, uuid.Nil, d, ""},
	}
	for _, c := range cases {
		n, err := Parse(c.raw)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if n.Recipient != c.recipient || n.LookupOrder != c.lookup || n.ID != c.id || n.Status != c.status {
			t.Errorf("%s: %+v", c.name, n)
		}
		// F-21: push faqat hodisa turi va ID
		pd := n.PushData()
		if len(pd) != 2 || pd["type"] != n.Event || pd["id"] != c.id.String() {
			t.Errorf("%s: push %v", c.name, pd)
		}
		// ro'yxat parametrlari ham faqat ID lar
		for k, v := range n.Params() {
			if _, err := uuid.Parse(v); err != nil || k == "" {
				t.Errorf("%s: params[%s]=%q UUID emas", c.name, k, v)
			}
		}
	}
	// WebSocket: holat o'zgarsa status + order_id
	n, _ := Parse(cases[0].raw)
	if m := n.WSMessage(); m["status"] != "searching" || m["order_id"] != s(o) {
		t.Errorf("ws: %v", m)
	}
}

func TestParsePoison(t *testing.T) {
	id := uuid.NewString()
	bad := map[string][]byte{
		"json":                []byte(`{`),
		"versiya":             []byte(`{"event":"order.created","version":2,"event_id":"` + id + `","payload":{}}`),
		"event_id":            []byte(`{"event":"order.created","version":1,"event_id":"x","payload":{}}`),
		"noma'lum hodisa":     ev("payment.paid", map[string]any{}),
		"payload yo'q":        []byte(`{"event":"order.created","version":1,"event_id":"` + id + `"}`),
		"passenger_id yo'q":   ev("order.created", map[string]any{"order_id": id}),
		"driver_id noto'g'ri": ev("wallet.limit_reached", map[string]any{"wallet_id": id, "driver_id": "x"}),
	}
	for name, raw := range bad {
		if _, err := Parse(raw); !errors.Is(err, ErrPoison) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
