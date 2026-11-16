package httpapi_test

import (
	"testing"

	"github.com/google/uuid"
)

// TestOwnershipMatrix (F-10) — har bir {id} endpointi uchun yagona tartib:
// noto'g'ri UUID formati -> 422, mavjud emas -> 404, boshqaning resursi -> 403 (reason: not_owner).
func TestOwnershipMatrix(t *testing.T) {
	e := newT(t)
	owner, stranger := e.passenger(), e.passenger()
	drv, otherDrv := e.driver(), e.driver()
	e.AddDrivers(drv.ID, otherDrv.ID)

	// Haydovchi qabul qilgan buyurtma (safar bor) va alohida qidiruvdagi buyurtma (taklif bor).
	taken := owner.order(t, nil)
	tr := drv.do("POST", acceptPath(drv.offerFor(t, taken.str("id"))), nil)
	if tr.Status != 200 {
		t.Fatalf("accept: %d %s", tr.Status, tr.Raw)
	}
	tripID := tr.str("id")
	open := owner.order(t, nil)
	offerID := drv.offerFor(t, open.str("id"))
	saved := owner.do("POST", "/api/v1/places/saved", map[string]any{"name": "Uy", "lat": 41.3, "lng": 69.2})
	if saved.Status != 201 {
		t.Fatalf("saved: %d %s", saved.Status, saved.Raw)
	}

	type route struct {
		method, prefix, suffix string
		body                   any
		foreigner              user
		ownID                  string
	}
	cancelBody := map[string]any{"reason": "other"}
	routes := map[string]route{
		"GET order":             {"GET", "/api/v1/orders/", "", nil, stranger, open.str("id")},
		"PATCH order":           {"PATCH", "/api/v1/orders/", "", map[string]any{"comment": "x"}, stranger, open.str("id")},
		"cancel order":          {"POST", "/api/v1/orders/", "/cancel", nil, stranger, open.str("id")},
		"rate order":            {"POST", "/api/v1/orders/", "/rate", map[string]any{"stars": 4}, stranger, taken.str("id")},
		"PUT rate":              {"PUT", "/api/v1/orders/", "/rate", map[string]any{"stars": 4}, stranger, taken.str("id")},
		"delete saved place":    {"DELETE", "/api/v1/places/saved/", "", nil, stranger, saved.str("id")},
		"accept offer":          {"POST", "/api/v1/trips/offers/", "/accept", nil, otherDrv, offerID},
		"reject offer":          {"POST", "/api/v1/trips/offers/", "/reject", nil, otherDrv, offerID},
		"arrive trip":           {"POST", "/api/v1/trips/", "/arrive", nil, otherDrv, tripID},
		"start trip":            {"POST", "/api/v1/trips/", "/start", nil, otherDrv, tripID},
		"complete trip":         {"POST", "/api/v1/trips/", "/complete", nil, otherDrv, tripID},
		"driver cancel trip":    {"POST", "/api/v1/trips/", "/cancel", cancelBody, otherDrv, tripID},
		"GET order (haydovchi)": {"GET", "/api/v1/orders/", "", nil, otherDrv, taken.str("id")},
	}
	for name, rt := range routes {
		t.Run(name, func(t *testing.T) {
			// egasi rolidagi foydalanuvchi (yo'lovchi yoki haydovchi) — 422/404 tekshiruvi uchun
			self := owner
			if rt.foreigner.ID == otherDrv.ID {
				self = drv
			}
			for _, bad := range []string{"abc", "00000000-0000-0000-0000-00000000000g", "urn:uuid:" + uuid.NewString()} {
				if r := self.do(rt.method, rt.prefix+bad+rt.suffix, rt.body); r.Status != 422 || r.errKey() != "VALIDATION_ERROR" {
					t.Errorf("format %q: %d %s", bad, r.Status, r.Raw)
				}
			}
			if r := self.do(rt.method, rt.prefix+uuid.NewString()+rt.suffix, rt.body); r.Status != 404 {
				t.Errorf("yo'q resurs: %d %s", r.Status, r.Raw)
			}
			if r := rt.foreigner.do(rt.method, rt.prefix+rt.ownID+rt.suffix, rt.body); r.Status != 403 || r.errParam("reason") != "not_owner" {
				t.Errorf("begona: %d %s", r.Status, r.Raw)
			}
		})
	}

	// Begona urinishlar hech narsani o'zgartirmagan: buyurtma hali qidiruvda, safar hali assigned.
	if r := owner.do("GET", "/api/v1/orders/"+open.str("id"), nil); r.str("status") != "searching" {
		t.Errorf("begona so'rovlardan keyin buyurtma: %s", r.Raw)
	}
	if r := drv.do("POST", "/api/v1/trips/"+tripID+"/arrive", nil); r.Status != 200 {
		t.Errorf("begona so'rovlardan keyin safar: %d %s", r.Status, r.Raw)
	}
}
