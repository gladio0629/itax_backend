// Package httpapi — Matching & Geo ichki HTTP API'si. Kong tashqariga chiqarmaydi; qo'shimcha himoya sifatida har bir
// so'rov umumiy sir (X-Internal-Token) bilan tekshiriladi.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/izgo/shared/go/authn"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/matching-geo/internal/apperr"
	"github.com/izgo/matching-geo/internal/service"
	"github.com/izgo/matching-geo/internal/store"
	"github.com/izgo/shared/geo"
)

const (
	InternalTokenHeader = "X-Internal-Token"
	maxBody             = 16 << 10
	maxFindLimit        = 100
	maxOfferHorizon     = 24 * time.Hour
)

// TokenVerifier — Identity access tokenini tekshiradi (shared/go/authn.Verifier).
type TokenVerifier interface {
	Verify(ctx context.Context, raw string) (*authn.Identity, error)
}

type Server struct {
	svc        *service.Service
	auth       TokenVerifier
	tokens     [][]byte
	maxRadiusM float64
	log        *slog.Logger
	now        func() time.Time
}

func New(svc *service.Service, internalTokens []string, auth TokenVerifier, maxRadiusM float64, log *slog.Logger) *Server {
	s := &Server{svc: svc, auth: auth, maxRadiusM: maxRadiusM, log: log, now: time.Now}
	for _, t := range internalTokens {
		s.tokens = append(s.tokens, []byte(strings.TrimSpace(t)))
	}
	return s
}

type ctxKey int

const keyRequestID ctxKey = iota

func requestID(ctx context.Context) string { id, _ := ctx.Value(keyRequestID).(string); return id }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Public (Kong: /api/v1/location/*) — haydovchi ilovasi GPS yuboradi; Bearer access token (aud=driver).
	mux.HandleFunc("POST /api/v1/location/update", s.driverAuthed(s.publicLocation))

	mux.HandleFunc("POST /internal/matching/location", s.internal(s.location))
	mux.HandleFunc("POST /internal/matching/find", s.internal(s.find))
	mux.HandleFunc("POST /internal/matching/route", s.internal(s.route))
	mux.HandleFunc("PUT /internal/matching/drivers/{id}/profile", s.internal(s.putProfile))
	// Driver servisi sinxronlashi (camelCase body, ixtiyoriy joylashuv) — PUT bilan bir xil Redis profili.
	mux.HandleFunc("POST /internal/matching/drivers/{id}/profile", s.internal(s.postProfile))
	mux.HandleFunc("PUT /internal/matching/drivers/{id}/status", s.internal(s.putStatus))
	mux.HandleFunc("/", s.internal(func(http.ResponseWriter, *http.Request) error { return apperr.NotFound() }))
	return s.recoverer(s.withRequestID(s.accessLog(mux)))
}

// driverAuthed — Bearer access token: imzo, muddat, issuer, blacklist (shared/go/authn) va rol = driver.
// Token yo'q yoki yaroqsiz — 401; yo'lovchi tokeni — 403 (reason: role); Redis xatosi — 500 (fail closed).
func (s *Server) driverAuthed(h func(http.ResponseWriter, *http.Request, *authn.Identity) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := func() error {
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || strings.TrimSpace(raw) == "" || s.auth == nil {
				return apperr.Unauthorized()
			}
			id, err := s.auth.Verify(r.Context(), strings.TrimSpace(raw))
			if errors.Is(err, authn.ErrInvalid) {
				return apperr.Unauthorized()
			}
			if err != nil {
				return err
			}
			if id.Role != authn.RoleDriver {
				return apperr.Forbidden("role")
			}
			return h(w, r, id)
		}()
		if err != nil {
			writeError(r.Context(), w, s.log, err)
		}
	}
}

// internal — ichki token tekshiruvi (vaqt bo'yicha o'zgarmas taqqoslash; barcha tokenlar tekshiriladi).
func (s *Server) internal(h func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get(InternalTokenHeader))
		ok := 0
		for _, t := range s.tokens {
			ok |= subtle.ConstantTimeCompare(got, t)
		}
		if ok != 1 {
			writeError(r.Context(), w, s.log, apperr.Forbidden("internal_only"))
			return
		}
		if err := h(w, r); err != nil {
			writeError(r.Context(), w, s.log, err)
		}
	}
}

// ---------------------------------------------------------------- DTO

type validator interface {
	validate(s *Server) map[string]string
}

func decode(w http.ResponseWriter, r *http.Request, s *Server, dst validator) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return apperr.Validation(jsonErrFields(err))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return apperr.Validation(map[string]string{"body": "invalid_json"})
	}
	if f := dst.validate(s); len(f) > 0 {
		return apperr.Validation(f)
	}
	return nil
}

func jsonErrFields(err error) map[string]string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return map[string]string{te.Field: "invalid_type"}
	}
	if s := err.Error(); strings.HasPrefix(s, "json: unknown field ") {
		return map[string]string{strings.Trim(strings.TrimPrefix(s, "json: unknown field "), `"`): "unknown_field"}
	}
	return map[string]string{"body": "invalid_json"}
}

type pointDTO struct {
	Lat *float64 `json:"lat"`
	Lng *float64 `json:"lng"`
}

func (p *pointDTO) check(f map[string]string, name string) {
	if p == nil {
		f[name] = "required"
		return
	}
	if p.Lat == nil || math.IsNaN(*p.Lat) || *p.Lat < -90 || *p.Lat > 90 {
		f[name+".lat"] = "invalid_value"
	}
	if p.Lng == nil || math.IsNaN(*p.Lng) || *p.Lng < -180 || *p.Lng > 180 {
		f[name+".lng"] = "invalid_value"
	}
}

func (p *pointDTO) geo() geo.Point { return geo.Point{Lat: *p.Lat, Lng: *p.Lng} }

type pointResp struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func oneOf(v string, list ...string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

var (
	carClasses   = []string{"economy", "comfort", "business", "minivan"}
	vehicleTypes = []string{"spark", "damas", "labo", "isuzu", "isuzu2", "fura"}
)

// ---- POST /internal/matching/location

type locationDTO struct {
	DriverID string   `json:"driver_id"`
	Lat      *float64 `json:"lat"`
	Lng      *float64 `json:"lng"`
}

func (d *locationDTO) validate(*Server) map[string]string {
	f := map[string]string{}
	if _, err := uuid.Parse(d.DriverID); err != nil {
		f["driver_id"] = "invalid_format"
	}
	(&pointDTO{Lat: d.Lat, Lng: d.Lng}).check(f, "")
	return fixDot(f)
}

// fixDot — bo'sh prefiksli ".lat" kalitlarini "lat" ga o'giradi.
func fixDot(f map[string]string) map[string]string {
	out := make(map[string]string, len(f))
	for k, v := range f {
		out[strings.TrimPrefix(k, ".")] = v
	}
	return out
}

func (s *Server) location(w http.ResponseWriter, r *http.Request) error {
	var in locationDTO
	if err := decode(w, r, s, &in); err != nil {
		return err
	}
	id, _ := uuid.Parse(in.DriverID)
	res, err := s.svc.UpdateLocation(r.Context(), id, *in.Lat, *in.Lng)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"suspicious": res.Suspicious})
	return nil
}

// ---- POST /api/v1/location/update (public)

// publicLocationDTO — haydovchi ID si body'da EMAS: tokendan olinadi (boshqa haydovchi nomidan yuborib bo'lmaydi).
type publicLocationDTO struct {
	Lat *float64 `json:"lat"`
	Lng *float64 `json:"lng"`
}

func (d *publicLocationDTO) validate(*Server) map[string]string {
	f := map[string]string{}
	(&pointDTO{Lat: d.Lat, Lng: d.Lng}).check(f, "")
	return fixDot(f)
}

// publicLocation — 204: `suspicious` (F-11 tezlik sakrashi) haydovchiga qaytarilmaydi, faqat log/anomaliyaga yoziladi.
func (s *Server) publicLocation(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	var in publicLocationDTO
	if err := decode(w, r, s, &in); err != nil {
		return err
	}
	if _, err := s.svc.UpdateLocation(r.Context(), id.UserID, *in.Lat, *in.Lng); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- POST /internal/matching/find

type findDTO struct {
	Type        string    `json:"type"`
	Seats       *int      `json:"seats"`
	CarClass    *string   `json:"car_class"`
	VehicleType *string   `json:"vehicle_type"`
	From        *pointDTO `json:"from"`
	RadiusM     *float64  `json:"radius_m"`
	Limit       *int      `json:"limit"`
	OrderID     *string   `json:"order_id"`
	ExpiresAt   *string   `json:"expires_at"`

	orderID   *uuid.UUID
	expiresAt *time.Time
}

func (d *findDTO) validate(s *Server) map[string]string {
	f := map[string]string{}
	switch store.OrderKind(d.Type) {
	case store.KindPassenger:
		if d.VehicleType != nil {
			f["vehicle_type"] = "only_for_delivery"
		}
		switch {
		case d.Seats == nil && d.CarClass == nil:
			f["seats"] = "seats_or_car_class_required"
		case d.Seats != nil && d.CarClass != nil:
			f["car_class"] = "conflicts_with_seats"
		}
	case store.KindDelivery:
		if d.Seats != nil {
			f["seats"] = "not_applicable_to_delivery"
		}
		if d.CarClass != nil {
			f["car_class"] = "not_applicable_to_delivery"
		}
		if d.VehicleType == nil {
			f["vehicle_type"] = "required"
		}
	case "":
		f["type"] = "required"
	default:
		f["type"] = "invalid_value"
	}
	if d.Seats != nil && (*d.Seats < 1 || *d.Seats > 4) {
		f["seats"] = "out_of_range"
	}
	if d.CarClass != nil && !oneOf(*d.CarClass, carClasses...) {
		f["car_class"] = "invalid_value"
	}
	if d.VehicleType != nil && !oneOf(*d.VehicleType, vehicleTypes...) {
		f["vehicle_type"] = "invalid_value"
	}
	d.From.check(f, "from")
	if d.RadiusM != nil && (math.IsNaN(*d.RadiusM) || *d.RadiusM < 1 || *d.RadiusM > s.maxRadiusM) {
		f["radius_m"] = "out_of_range"
	}
	if d.Limit != nil && (*d.Limit < 1 || *d.Limit > maxFindLimit) {
		f["limit"] = "out_of_range"
	}
	if d.OrderID != nil {
		if id, err := uuid.Parse(*d.OrderID); err != nil {
			f["order_id"] = "invalid_format"
		} else {
			d.orderID = &id
		}
	}
	if d.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *d.ExpiresAt)
		switch {
		case d.OrderID == nil:
			f["expires_at"] = "requires_order_id"
		case err != nil:
			f["expires_at"] = "invalid_format"
		case !t.After(s.now()):
			f["expires_at"] = "in_past"
		case t.After(s.now().Add(maxOfferHorizon)):
			f["expires_at"] = "too_far"
		default:
			t = t.UTC()
			d.expiresAt = &t
		}
	}
	return f
}

type matchResp struct {
	DriverID       uuid.UUID  `json:"driver_id"`
	DistanceM      int        `json:"distance_m"`
	Location       pointResp  `json:"location"`
	PickupETAs     int        `json:"pickup_eta_s"`
	OfferID        *uuid.UUID `json:"offer_id,omitempty"`
	OfferExpiresAt *time.Time `json:"offer_expires_at,omitempty"`
}

func (s *Server) find(w http.ResponseWriter, r *http.Request) error {
	var in findDTO
	if err := decode(w, r, s, &in); err != nil {
		return err
	}
	req := service.FindRequest{
		Kind: store.OrderKind(in.Type), Seats: in.Seats, CarClass: in.CarClass, VehicleType: in.VehicleType,
		From: in.From.geo(), RadiusM: in.RadiusM, OrderID: in.orderID, ExpiresAt: in.expiresAt, TraceID: requestID(r.Context()),
	}
	if in.Limit != nil {
		req.Limit = *in.Limit
	}
	ms, err := s.svc.Find(r.Context(), req)
	if err != nil {
		return err
	}
	out := make([]matchResp, 0, len(ms))
	for _, m := range ms {
		out = append(out, matchResp{DriverID: m.DriverID, DistanceM: m.DistanceM, Location: pointResp{m.Location.Lat, m.Location.Lng},
			PickupETAs: m.PickupETAs, OfferID: m.OfferID, OfferExpiresAt: m.OfferExpiresAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"drivers": out})
	return nil
}

// ---- POST /internal/matching/route

type routeDTO struct {
	From *pointDTO `json:"from"`
	To   *pointDTO `json:"to"`
}

func (d *routeDTO) validate(*Server) map[string]string {
	f := map[string]string{}
	d.From.check(f, "from")
	d.To.check(f, "to")
	return f
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) error {
	var in routeDTO
	if err := decode(w, r, s, &in); err != nil {
		return err
	}
	rt, err := s.svc.Route(r.Context(), in.From.geo(), in.To.geo())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]int{"distance_m": rt.DistanceM, "duration_s": rt.DurationS})
	return nil
}

// ---- PUT /internal/matching/drivers/{id}/profile | status

func driverID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, apperr.Validation(map[string]string{"id": "invalid_format"})
	}
	return id, nil
}

type profileDTO struct {
	State       string  `json:"state"`
	Seats       *int    `json:"seats"`
	CarClass    *string `json:"car_class"`
	VehicleType *string `json:"vehicle_type"`
}

func (d *profileDTO) validate(*Server) map[string]string {
	f := map[string]string{}
	if d.State == "" {
		f["state"] = "required"
	} else if !oneOf(d.State, store.DriverStates...) {
		f["state"] = "invalid_value"
	}
	if d.Seats != nil && (*d.Seats < 1 || *d.Seats > 8) {
		f["seats"] = "out_of_range"
	}
	if d.CarClass != nil && !oneOf(*d.CarClass, carClasses...) {
		f["car_class"] = "invalid_value"
	}
	if d.VehicleType != nil && !oneOf(*d.VehicleType, vehicleTypes...) {
		f["vehicle_type"] = "invalid_value"
	}
	return f
}

func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := driverID(r)
	if err != nil {
		return err
	}
	var in profileDTO
	if err := decode(w, r, s, &in); err != nil {
		return err
	}
	p := store.Profile{State: in.State}
	if in.Seats != nil {
		p.Seats = *in.Seats
	}
	if in.CarClass != nil {
		p.CarClass = *in.CarClass
	}
	if in.VehicleType != nil {
		p.VehicleType = *in.VehicleType
	}
	if err := s.svc.SetProfile(r.Context(), id, p); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- POST /internal/matching/drivers/{id}/profile (Driver servisi sinxronlashi)

// syncProfileDTO — Driver servisi yuboradigan shakl. `status` = Driver holati (active, blocked, ...), Matching'ning
// available/busy/offline holati EMAS. `driverId` path'dagi ID bilan teng bo'lishi shart (identity user ID).
// Profil to'liq almashtiriladi (PUT bilan bir xil); `location` berilsa joylashuv ham yangilanadi.
type syncProfileDTO struct {
	DriverID    string    `json:"driverId"`
	Status      string    `json:"status"`
	Seats       *int      `json:"seats"`
	CarClass    *string   `json:"carClass"`
	VehicleType *string   `json:"vehicleType"`
	Location    *pointDTO `json:"location"`

	pathID uuid.UUID
}

func (d *syncProfileDTO) validate(*Server) map[string]string {
	f := map[string]string{}
	if id, err := uuid.Parse(d.DriverID); err != nil {
		f["driverId"] = "invalid_format"
	} else if id != d.pathID {
		f["driverId"] = "path_mismatch"
	}
	if d.Status == "" {
		f["status"] = "required"
	} else if !oneOf(d.Status, store.DriverStates...) {
		f["status"] = "invalid_value"
	}
	if d.Seats != nil && (*d.Seats < 1 || *d.Seats > 8) {
		f["seats"] = "out_of_range"
	}
	if d.CarClass != nil && !oneOf(*d.CarClass, carClasses...) {
		f["carClass"] = "invalid_value"
	}
	if d.VehicleType != nil && !oneOf(*d.VehicleType, vehicleTypes...) {
		f["vehicleType"] = "invalid_value"
	}
	if d.Location != nil {
		d.Location.check(f, "location")
	}
	return f
}

func (s *Server) postProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := driverID(r)
	if err != nil {
		return err
	}
	in := syncProfileDTO{pathID: id}
	if err := decode(w, r, s, &in); err != nil {
		return err
	}
	p := store.Profile{State: in.Status}
	if in.Seats != nil {
		p.Seats = *in.Seats
	}
	if in.CarClass != nil {
		p.CarClass = *in.CarClass
	}
	if in.VehicleType != nil {
		p.VehicleType = *in.VehicleType
	}
	if err := s.svc.SetProfile(r.Context(), id, p); err != nil {
		return err
	}
	if in.Location != nil {
		if _, err := s.svc.UpdateLocation(r.Context(), id, *in.Location.Lat, *in.Location.Lng); err != nil {
			return err
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type statusDTO struct {
	Status string `json:"status"`
}

func (d *statusDTO) validate(*Server) map[string]string {
	f := map[string]string{}
	switch d.Status {
	case "":
		f["status"] = "required"
	case string(store.Available), string(store.Offline):
	case string(store.Busy):
		f["status"] = "set_by_trip_events" // busy faqat order.accepted dan o'rnatiladi
	default:
		f["status"] = "invalid_value"
	}
	return f
}

func (s *Server) putStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := driverID(r)
	if err != nil {
		return err
	}
	var in statusDTO
	if err := decode(w, r, s, &in); err != nil {
		return err
	}
	switch err := s.svc.SetStatus(r.Context(), id, store.Status(in.Status)); {
	case errors.Is(err, service.ErrBusy):
		return apperr.Forbidden("driver_busy")
	case err != nil:
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": in.Status})
	return nil
}

// ---------------------------------------------------------------- javob va xatolar

type errorBody struct {
	Error struct {
		Key    string         `json:"key"`
		Params map[string]any `json:"params"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(ctx context.Context, w http.ResponseWriter, log *slog.Logger, err error) {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		rid := requestID(ctx)
		log.ErrorContext(ctx, "ichki xato", "request_id", rid, "err", err)
		ae = apperr.New(http.StatusInternalServerError, "INTERNAL_ERROR", map[string]any{"request_id": rid})
	}
	var b errorBody
	b.Error.Key, b.Error.Params = ae.Key, ae.Params
	writeJSON(w, ae.Status, b)
}

// ---------------------------------------------------------------- middleware

func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			b := make([]byte, 4)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), keyRequestID, id)))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" {
			return
		}
		s.log.InfoContext(r.Context(), "http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"dur_ms", time.Since(start).Milliseconds(), "request_id", requestID(r.Context()))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeError(r.Context(), w, s.log, fmt.Errorf("panic: %v", rec))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
