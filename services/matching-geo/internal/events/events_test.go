package events

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildOfferCreatedMatchesSchema(t *testing.T) {
	o := OfferCreated{OfferID: uuid.New(), OrderID: uuid.New(), DriverID: uuid.New(), ETAs: 300,
		ExpiresAt: time.Date(2026, 11, 12, 10, 2, 0, 0, time.FixedZone("x", 5*3600))}
	raw, err := BuildOfferCreated(o, time.Now(), "trace-1")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	// shared/events/offer.created.json: additionalProperties=false -> aynan shu kalitlar
	for _, k := range []string{"event", "version", "event_id", "occurred_at", "trace_id", "payload"} {
		if _, ok := m[k]; !ok {
			t.Errorf("envelope'da %q yo'q", k)
		}
	}
	if len(m) != 6 || m["event"] != "offer.created" || m["version"] != float64(1) || m["trace_id"] != "trace-1" {
		t.Errorf("envelope: %s", raw)
	}
	pl := m["payload"].(map[string]any)
	if len(pl) != 5 || pl["offer_id"] != o.OfferID.String() || pl["order_id"] != o.OrderID.String() ||
		pl["driver_id"] != o.DriverID.String() || pl["eta_s"] != float64(300) || pl["expires_at"] != "2026-11-12T05:02:00Z" {
		t.Errorf("payload: %v", pl)
	}
	if _, err := uuid.Parse(m["event_id"].(string)); err != nil {
		t.Errorf("event_id: %v", err)
	}
	// trace bo'sh bo'lsa kalit tushib qoladi (ixtiyoriy maydon)
	raw, _ = BuildOfferCreated(o, time.Now(), "")
	var m2 map[string]any
	_ = json.Unmarshal(raw, &m2)
	if _, ok := m2["trace_id"]; ok {
		t.Error("bo'sh trace_id yuborildi")
	}
}

func TestParseTripEvents(t *testing.T) {
	d, tr := uuid.New(), uuid.New()
	body := func(event string, version int, driver, trip string) []byte {
		b, _ := json.Marshal(map[string]any{"event": event, "version": version, "event_id": uuid.NewString(), "occurred_at": "2026-11-12T10:00:00Z",
			"payload": map[string]any{"driver_id": driver, "trip_id": trip, "order_id": uuid.NewString()}})
		return b
	}
	ref, err := ParseOrderAccepted(body("order.accepted", 1, d.String(), tr.String()))
	if err != nil || ref.DriverID != d || ref.TripID != tr {
		t.Fatalf("%+v %v", ref, err)
	}
	if ref, err := ParseTripCompleted(body("trip.completed", 1, d.String(), tr.String())); err != nil || ref.TripID != tr {
		t.Fatalf("%+v %v", ref, err)
	}
	bad := map[string][]byte{
		"buzuq json":          []byte(`{`),
		"bo'sh":               []byte(``),
		"boshqa hodisa":       body("trip.completed", 1, d.String(), tr.String()),
		"versiya 2":           body("order.accepted", 2, d.String(), tr.String()),
		"driver_id noto'g'ri": body("order.accepted", 1, "x", tr.String()),
		"trip_id yo'q":        body("order.accepted", 1, d.String(), ""),
		"payload yo'q":        []byte(`{"event":"order.accepted","version":1}`),
	}
	for name, raw := range bad {
		if _, err := ParseOrderAccepted(raw); !errors.Is(err, ErrPoison) {
			t.Errorf("%s: err=%v, ErrPoison kutilgan", name, err)
		}
	}
}
