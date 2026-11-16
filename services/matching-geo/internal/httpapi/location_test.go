package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/izgo/matching-geo/internal/service"
	"github.com/izgo/matching-geo/internal/store"
)

const jwtIssuer = "izgo-identity-test"

// accessToken — Identity chiqaradigan access token ko'rinishida (typ=access, sid, role, aud).
func (e *env) accessToken(sub uuid.UUID, role, aud, sid string, ttl time.Duration) string {
	e.t.Helper()
	c := jwt.MapClaims{"iss": jwtIssuer, "sub": sub.String(), "aud": []string{aud}, "exp": time.Now().Add(ttl).Unix(),
		"typ": "access", "sid": sid, "role": role}
	s, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c).SignedString(e.jwtKey)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

// public — Bearer bilan so'rov (X-Internal-Token YO'Q, xuddi Kong orqali kelgandek).
func (e *env) public(method, path, bearer string, body any) resp {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewBufferString(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Raw: raw}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.Body)
	}
	return out
}

func (r resp) reason() any {
	e, _ := r.Body["error"].(map[string]any)
	p, _ := e["params"].(map[string]any)
	return p["reason"]
}

func TestPublicLocation(t *testing.T) {
	e := newEnv(t)
	driver := uuid.New()
	sid := uuid.NewString()
	tok := e.accessToken(driver, "driver", "driver", sid, time.Minute)
	pt := map[string]any{"lat": baseLat, "lng": baseLng}

	// haydovchi o'z joylashuvini yuboradi: 204, ID tokendan olinadi
	r := e.public("POST", "/api/v1/location/update", tok, pt)
	if r.Status != 204 || len(r.Raw) != 0 {
		t.Fatalf("location: %d %s", r.Status, r.Raw)
	}
	// ichki find shu haydovchini ko'radi (profil bo'lmasa ham joylashuv saqlangan bo'lishi kerak)
	if n := e.mr.Keys(); len(n) == 0 {
		t.Fatal("joylashuv Redis'ga yozilmadi")
	}

	// F-11 tezlik sakrashi: javob baribir 204 — suspicious haydovchiga oshkor qilinmaydi
	e.clk.Advance(time.Second)
	if r = e.public("POST", "/api/v1/location/update", tok, map[string]any{"lat": baseLat + 1, "lng": baseLng}); r.Status != 204 || len(r.Raw) != 0 {
		t.Fatalf("sakrash: %d %s", r.Status, r.Raw)
	}

	// body'da driver_id yuborib boshqa haydovchi nomidan yozib bo'lmaydi
	if r = e.public("POST", "/api/v1/location/update", tok, map[string]any{"lat": baseLat, "lng": baseLng, "driver_id": uuid.NewString()}); r.Status != 422 {
		t.Fatalf("driver_id body'da: %d %s", r.Status, r.Raw)
	}
	for _, body := range []any{map[string]any{"lat": 91.0, "lng": baseLng}, map[string]any{"lat": baseLat}, "{"} {
		if r = e.public("POST", "/api/v1/location/update", tok, body); r.Status != 422 || r.errKey() != "VALIDATION_ERROR" {
			t.Fatalf("%v: %d %s", body, r.Status, r.Raw)
		}
	}

	// token muammolari: 401
	for name, bearer := range map[string]string{
		"yo'q":           "",
		"buzuq":          "abc.def.ghi",
		"muddati o'tgan": e.accessToken(driver, "driver", "driver", uuid.NewString(), -time.Minute),
		"admin":          e.accessToken(driver, "superadmin", "admin", uuid.NewString(), time.Minute),
	} {
		if r = e.public("POST", "/api/v1/location/update", bearer, pt); r.Status != 401 || r.errKey() != "AUTH_TOKEN_EXPIRED" {
			t.Fatalf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	// yo'lovchi tokeni: 403 role
	if r = e.public("POST", "/api/v1/location/update", e.accessToken(uuid.New(), "passenger", "client", uuid.NewString(), time.Minute), pt); r.Status != 403 || r.reason() != "role" {
		t.Fatalf("yo'lovchi: %d %s", r.Status, r.Raw)
	}
	// logout (Identity bl:sid yozadi) — token darhol yaroqsiz
	e.mr.Set("bl:sid:"+sid, "1")
	if r = e.public("POST", "/api/v1/location/update", tok, pt); r.Status != 401 {
		t.Fatalf("blacklist: %d %s", r.Status, r.Raw)
	}

	// ichki yo'llar Bearer bilan ochilmaydi (faqat X-Internal-Token)
	if r = e.public("POST", "/internal/matching/location", e.accessToken(driver, "driver", "driver", uuid.NewString(), time.Minute),
		map[string]any{"driver_id": driver.String(), "lat": baseLat, "lng": baseLng}); r.Status != 403 || r.reason() != "internal_only" {
		t.Fatalf("internal Bearer bilan: %d %s", r.Status, r.Raw)
	}
}

// TestPublicLocationStationaryOffline (F-11) — haydovchi ilovasi 5 daqiqa aynan bir xil nuqta yuborsa, haydovchi
// qidiruvdan chiqadi (WARN gps_stationary bir marta); nuqta o'zgarganda qaytadi.
func TestPublicLocationStationaryOffline(t *testing.T) {
	e := newEnv(t, func(st *store.Options, svc *service.Options) {
		st.StationaryAfter = 5 * time.Minute
		svc.StationaryAfter = 5 * time.Minute
	})
	driver := uuid.New()
	e.profile(driver, map[string]any{"state": "active", "seats": 4, "car_class": "economy"})
	tok := e.accessToken(driver, "driver", "driver", uuid.NewString(), time.Hour)
	send := func(lat float64) {
		t.Helper()
		if r := e.public("POST", "/api/v1/location/update", tok, map[string]any{"lat": lat, "lng": baseLng}); r.Status != 204 {
			t.Fatalf("location: %d %s", r.Status, r.Raw)
		}
	}
	found := func() bool { return slices.Contains(e.findIDs(nil), driver.String()) }

	steps := []struct {
		name      string
		advance   time.Duration
		lat       float64
		wantFound bool
	}{
		{"birinchi nuqta", 0, north(1), true},
		{"4 daqiqa o'zgarmas", 4 * time.Minute, north(1), true},
		{"5 daqiqa o'zgarmas -> offline", time.Minute, north(1), false},
		{"hali ham o'zgarmas", time.Minute, north(1), false},
		{"siljidi -> qaytdi", time.Minute, north(1.05), true},
	}
	for _, s := range steps {
		e.clk.Advance(s.advance)
		send(s.lat)
		if got := found(); got != s.wantFound {
			t.Fatalf("%s: qidiruvda=%v, kutilgan %v", s.name, got, s.wantFound)
		}
	}
	if n := strings.Count(e.logs.String(), "gps_stationary"); n != 1 {
		t.Fatalf("gps_stationary log %d marta, kutilgan 1:\n%s", n, e.logs.String())
	}
}
