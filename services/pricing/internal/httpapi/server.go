// Package httpapi — Pricing'ning ichki HTTP API'si. Kong tashqariga chiqarmaydi; qo'shimcha himoya sifatida
// har bir so'rov umumiy sir (X-Internal-Token) bilan tekshiriladi.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/izgo/pricing/internal/apperr"
	"github.com/izgo/pricing/internal/calc"
	"github.com/izgo/pricing/internal/service"
	"github.com/izgo/pricing/internal/tariff"
	"github.com/izgo/shared/geo"
)

const (
	InternalTokenHeader = "X-Internal-Token"
	maxBody             = 16 << 10
)

type Server struct {
	svc    *service.Service
	tokens [][]byte
	log    *slog.Logger
}

func New(svc *service.Service, internalTokens []string, log *slog.Logger) *Server {
	s := &Server{svc: svc, log: log}
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
	mux.HandleFunc("POST /internal/pricing/calculate", s.internal(s.calculate))
	mux.HandleFunc("/", s.internal(func(http.ResponseWriter, *http.Request) error { return apperr.NotFound() }))
	return s.recoverer(s.withRequestID(s.accessLog(mux)))
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

// ---- POST /internal/pricing/calculate

type pointDTO struct {
	Lat *float64 `json:"lat"`
	Lng *float64 `json:"lng"`
}

type calculateDTO struct {
	Type        string    `json:"type"`
	Seats       *int      `json:"seats"`
	CarClass    *string   `json:"car_class"`
	VehicleType *string   `json:"vehicle_type"`
	From        *pointDTO `json:"from"`
	To          *pointDTO `json:"to"`
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

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (d *calculateDTO) validate() map[string]string {
	f := map[string]string{}
	switch calc.OrderType(d.Type) {
	case calc.Passenger:
		if d.VehicleType != nil {
			f["vehicle_type"] = "only_for_delivery"
		}
		switch {
		case d.Seats == nil && d.CarClass == nil:
			f["seats"] = "seats_or_car_class_required"
		case d.Seats != nil && d.CarClass != nil:
			f["car_class"] = "conflicts_with_seats"
		}
	case calc.Delivery:
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
	if d.CarClass != nil && !contains(tariff.PassengerClasses, *d.CarClass) {
		f["car_class"] = "invalid_value"
	}
	if d.VehicleType != nil && !contains(tariff.VehicleTypes, *d.VehicleType) {
		f["vehicle_type"] = "invalid_value"
	}
	d.From.check(f, "from")
	d.To.check(f, "to")
	return f
}

type calculateResp struct {
	Price         int64 `json:"price"`      // tiyin
	Commission    int64 `json:"commission"` // tiyin
	DistanceM     int   `json:"distance_m"`
	DurationS     int   `json:"duration_s"`
	TariffVersion int   `json:"tariff_version"`
}

func (s *Server) calculate(w http.ResponseWriter, r *http.Request) error {
	var in calculateDTO
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return apperr.Validation(jsonErrFields(err))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return apperr.Validation(map[string]string{"body": "invalid_json"})
	}
	if f := in.validate(); len(f) > 0 {
		return apperr.Validation(f)
	}
	q, err := s.svc.Calculate(r.Context(), service.Request{
		Type: calc.OrderType(in.Type), Seats: in.Seats, CarClass: in.CarClass, VehicleType: in.VehicleType,
		From: geo.Point{Lat: *in.From.Lat, Lng: *in.From.Lng}, To: geo.Point{Lat: *in.To.Lat, Lng: *in.To.Lng},
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, calculateResp{Price: q.Price, Commission: q.Commission, DistanceM: q.DistanceM, DurationS: q.DurationS, TariffVersion: q.TariffVersion})
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

// ---- javob va xatolar

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

// ---- middleware

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
