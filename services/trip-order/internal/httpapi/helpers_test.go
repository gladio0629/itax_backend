package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

type resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
}

func (r resp) errKey() string {
	e, _ := r.Body["error"].(map[string]any)
	k, _ := e["key"].(string)
	return k
}

func (r resp) errParam(name string) any {
	e, _ := r.Body["error"].(map[string]any)
	p, _ := e["params"].(map[string]any)
	return p[name]
}

func (r resp) str(k string) string { s, _ := r.Body[k].(string); return s }

func (r resp) obj(k string) map[string]any { m, _ := r.Body[k].(map[string]any); return m }

// api — foydalanuvchi nomidan so'rov yuboruvchi yordamchi.
type api struct {
	t   *testing.T
	e   *testenv.Env
	tok string
	hdr map[string]string
}

func (e *tenv) as(tok string) *api { return &api{t: e.T, e: e.Env, tok: tok} }

func (a *api) with(k, v string) *api {
	h := map[string]string{}
	for kk, vv := range a.hdr {
		h[kk] = vv
	}
	h[k] = v
	return &api{t: a.t, e: a.e, tok: a.tok, hdr: h}
}

func (a *api) do(method, path string, body any) resp {
	a.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewBufferString(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			a.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, a.e.Server.URL+path, rd)
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.tok != "" {
		req.Header.Set("Authorization", "Bearer "+a.tok)
	}
	for k, v := range a.hdr {
		req.Header.Set(k, v)
	}
	res, err := testenv.Client.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: raw}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out.Body); err != nil {
			a.t.Fatalf("javob JSON emas (%d): %q", res.StatusCode, raw)
		}
	}
	return out
}

// tenv — Env + tez-tez ishlatiladigan sahnalar.
type tenv struct{ *testenv.Env }

func newT(t *testing.T, opts ...testenv.Option) *tenv { return &tenv{testenv.New(t, opts...)} }

type user struct {
	ID uuid.UUID
	*api
}

func (e *tenv) passenger() user {
	id := uuid.New()
	return user{id, e.as(e.Token("passenger", id))}
}

func (e *tenv) driver() user {
	id := uuid.New()
	return user{id, e.as(e.Token("driver", id))}
}

// Toshkent ichidagi ikki nuqta (~3 km)
func trip(extra map[string]any) map[string]any {
	b := map[string]any{
		"type": "passenger", "seats": 2, "payment_method": "cash",
		"from": map[string]any{"lat": 41.3111, "lng": 69.2797, "address": "Amir Temur xiyoboni"},
		"to":   map[string]any{"lat": 41.2995, "lng": 69.2401},
	}
	for k, v := range extra {
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
	}
	return b
}

// order yaratadi va 201 ni tekshiradi.
func (u user) order(t *testing.T, extra map[string]any) resp {
	t.Helper()
	r := u.do("POST", "/api/v1/orders", trip(extra))
	if r.Status != 201 {
		t.Fatalf("buyurtma yaratilmadi: %d %s", r.Status, r.Raw)
	}
	return r
}

// firstOffer — haydovchining birinchi taklifi ID si.
func (u user) offerFor(t *testing.T, orderID string) string {
	t.Helper()
	r := u.do("GET", "/api/v1/trips/offers?limit=100", nil)
	if r.Status != 200 {
		t.Fatalf("offers: %d %s", r.Status, r.Raw)
	}
	for _, it := range r.Body["data"].([]any) {
		m := it.(map[string]any)
		if m["order_id"] == orderID {
			return m["id"].(string)
		}
	}
	t.Fatalf("%s buyurtma uchun taklif topilmadi: %s", orderID, r.Raw)
	return ""
}

func (e *tenv) advance(d time.Duration) { e.Clock.Advance(d) }

func testenvPassengerLimit(n int) testenv.Option { return testenv.PassengerLimit(n) }
