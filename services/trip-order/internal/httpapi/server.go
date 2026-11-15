// Package httpapi — Trip & Order servisining HTTP qatlami (net/http, Go 1.22 marshrutlash).
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/shared/go/authn"
	"github.com/izgo/trip-order/internal/apperr"
	"github.com/izgo/trip-order/internal/service"
)

type Server struct {
	svc *service.Service
	ver *authn.Verifier
	log *slog.Logger
}

func New(svc *service.Service, ver *authn.Verifier, log *slog.Logger) *Server {
	return &Server{svc: svc, ver: ver, log: log}
}

type ctxKey int

const keyRequestID ctxKey = iota

func requestID(ctx context.Context) string {
	id, _ := ctx.Value(keyRequestID).(string)
	return id
}

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (s *Server) handle(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeError(r.Context(), w, s.log, err)
		}
	}
}

// authed — Bearer access token + rol. role: authn.RolePassenger | authn.RoleDriver | "" (ikkalasi).
// X-App sarlavhasi berilgan bo'lsa token audience'iga mos bo'lishi shart (client <-> passenger, driver <-> driver).
func (s *Server) authed(role string, h func(w http.ResponseWriter, r *http.Request, id *authn.Identity) error) http.HandlerFunc {
	return s.handle(func(w http.ResponseWriter, r *http.Request) error {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(raw) == "" {
			return apperr.Unauthorized()
		}
		id, err := s.ver.Verify(r.Context(), strings.TrimSpace(raw))
		if errors.Is(err, authn.ErrInvalid) {
			return apperr.Unauthorized()
		}
		if err != nil {
			return err
		}
		if app := r.Header.Get("X-App"); app != "" && app != id.Audience {
			return apperr.Forbidden("app_mismatch", nil)
		}
		if role != "" && id.Role != role {
			return apperr.Forbidden("role", nil)
		}
		return h(w, r, id)
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	const P, D, ANY = authn.RolePassenger, authn.RoleDriver, ""
	mux.HandleFunc("POST /api/v1/orders", s.authed(P, s.createOrder))
	mux.HandleFunc("POST /api/v1/orders/quote", s.authed(P, s.quote))
	mux.HandleFunc("GET /api/v1/orders/seat-map", s.authed(P, s.seatMap))

	// Ma'lumotnomalar (token shart emas) va manzil qidiruvi (server tomonida; token kerak — ochiq geocoder bo'lib qolmasin).
	mux.HandleFunc("GET /api/v1/cities", s.handle(s.cities))
	mux.HandleFunc("GET /api/v1/routes/popular", s.handle(s.popularRoutes))
	mux.HandleFunc("GET /api/v1/delivery/vehicles", s.handle(s.deliveryVehicles))
	mux.HandleFunc("GET /api/v1/places/search", s.authed(ANY, s.placesSearch))
	mux.HandleFunc("GET /api/v1/places/reverse", s.authed(ANY, s.placesReverse))
	mux.HandleFunc("GET /api/v1/places/saved", s.authed(P, s.savedList))
	mux.HandleFunc("POST /api/v1/places/saved", s.authed(P, s.savedCreate))
	mux.HandleFunc("DELETE /api/v1/places/saved/{id}", s.authed(P, s.savedDelete))
	mux.HandleFunc("GET /api/v1/orders/history", s.authed(P, s.history))
	mux.HandleFunc("GET /api/v1/orders/{id}", s.authed(ANY, s.getOrder))
	mux.HandleFunc("PATCH /api/v1/orders/{id}", s.authed(P, s.updateOrder))
	mux.HandleFunc("POST /api/v1/orders/{id}/cancel", s.authed(P, s.cancelOrder))
	mux.HandleFunc("POST /api/v1/orders/{id}/rate", s.authed(P, s.rate))
	mux.HandleFunc("PUT /api/v1/orders/{id}/rate", s.authed(P, s.updateRate))

	mux.HandleFunc("GET /api/v1/trips/offers", s.authed(D, s.offers))
	mux.HandleFunc("POST /api/v1/trips/offers/{id}/accept", s.authed(D, s.acceptOffer))
	mux.HandleFunc("POST /api/v1/trips/{id}/start", s.authed(D, s.startTrip))
	mux.HandleFunc("POST /api/v1/trips/{id}/complete", s.authed(D, s.completeTrip))

	mux.HandleFunc("/", s.handle(func(http.ResponseWriter, *http.Request) error { return apperr.NotFound() }))
	return s.recoverer(s.withRequestID(s.accessLog(mux)))
}

// pathID — yo'ldagi {id}. Noto'g'ri UUID ham "topilmadi" (shartnomada bu endpointlarda 422 yo'q).
func pathID(r *http.Request) (uuid.UUID, error) {
	raw := r.PathValue("id")
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, apperr.OrderNotFound(raw)
	}
	return id, nil
}

func pageParams(r *http.Request) (cursor string, limit int, err error) {
	q := r.URL.Query()
	cursor = q.Get("cursor")
	if len(cursor) > 512 {
		return "", 0, apperr.Validation(map[string]string{"cursor": "invalid"})
	}
	limit = 20
	if v := q.Get("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > 100 {
			return "", 0, apperr.Validation(map[string]string{"limit": "out_of_range"})
		}
		limit = n
	}
	return cursor, limit, nil
}

// ---- buyurtmalar

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	var in createOrderDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	var key *string
	if k := r.Header.Get("Idempotency-Key"); k != "" {
		if _, err := uuid.Parse(k); err != nil {
			return apperr.Validation(map[string]string{"idempotency-key": "invalid_format"})
		}
		key = &k
	}
	o, err := s.svc.CreateOrder(r.Context(), id.UserID, in.input(), key, requestID(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, newOrderResp(o))
	return nil
}

func (s *Server) history(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	cursor, limit, err := pageParams(r)
	if err != nil {
		return err
	}
	p, err := s.svc.History(r.Context(), id.UserID, cursor, limit)
	if err != nil {
		return err
	}
	out := page[orderResp]{Data: make([]orderResp, 0, len(p.Items)), Cursor: p.Cursor}
	for _, o := range p.Items {
		out.Data = append(out.Data, newOrderResp(o))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	oid, err := pathID(r)
	if err != nil {
		return err
	}
	o, err := s.svc.GetOrder(r.Context(), service.Actor{ID: id.UserID, IsDriver: id.Role == authn.RoleDriver}, oid)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newOrderResp(o))
	return nil
}

func (s *Server) updateOrder(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	oid, err := pathID(r)
	if err != nil {
		return err
	}
	var in updateOrderDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	o, err := s.svc.UpdateOrder(r.Context(), id.UserID, oid, in.input())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newOrderResp(o))
	return nil
}

func (s *Server) cancelOrder(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	oid, err := pathID(r)
	if err != nil {
		return err
	}
	var in cancelDTO
	if err := decodeOptional(w, r, &in); err != nil {
		return err
	}
	o, err := s.svc.CancelOrder(r.Context(), id.UserID, oid, in.Reason, in.Comment)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newOrderResp(o))
	return nil
}

// ---- baho

func (s *Server) rate(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	oid, err := pathID(r)
	if err != nil {
		return err
	}
	var in ratingDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	rt, err := s.svc.Rate(r.Context(), id.UserID, oid, in.input())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, newRatingResp(rt))
	return nil
}

func (s *Server) updateRate(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	oid, err := pathID(r)
	if err != nil {
		return err
	}
	var in ratingDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	rt, err := s.svc.UpdateRating(r.Context(), id.UserID, oid, in.input())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newRatingResp(rt))
	return nil
}

// ---- takliflar va safar (haydovchi)

func (s *Server) offers(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	cursor, limit, err := pageParams(r)
	if err != nil {
		return err
	}
	p, err := s.svc.Offers(r.Context(), id.UserID, cursor, limit)
	if err != nil {
		return err
	}
	out := page[offerResp]{Data: make([]offerResp, 0, len(p.Items)), Cursor: p.Cursor}
	for _, v := range p.Items {
		out.Data = append(out.Data, newOfferResp(v))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) acceptOffer(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	oid, err := pathID(r)
	if err != nil {
		return err
	}
	t, err := s.svc.AcceptOffer(r.Context(), id.UserID, oid, requestID(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newTripResp(t))
	return nil
}

func (s *Server) startTrip(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	tid, err := pathID(r)
	if err != nil {
		return err
	}
	t, err := s.svc.StartTrip(r.Context(), id.UserID, tid)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newTripResp(t))
	return nil
}

func (s *Server) completeTrip(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	tid, err := pathID(r)
	if err != nil {
		return err
	}
	t, err := s.svc.CompleteTrip(r.Context(), id.UserID, tid, requestID(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newTripResp(t))
	return nil
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

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

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
