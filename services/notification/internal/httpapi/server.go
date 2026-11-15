// Package httpapi — Notification HTTP qatlami: foydalanuvchi API (/api/v1/notifications), ichki API (/internal/notification/*)
// va WebSocket (/ws). Ichki API Kong tashqariga chiqarmaydi; qo'shimcha himoya — X-Internal-Token.
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
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/notification/internal/apperr"
	"github.com/izgo/notification/internal/authn"
	"github.com/izgo/notification/internal/sms"
	"github.com/izgo/notification/internal/store"
	"github.com/redis/go-redis/v9"
)

const (
	InternalTokenHeader = "X-Internal-Token"
	maxBody             = 16 << 10
)

type Options struct {
	InternalTokens []string
	WSMaxConns     int
	WSRevokeCheck  time.Duration
	WSPingInterval time.Duration
}

type Server struct {
	st     *store.Store
	rdb    *redis.Client
	ver    *authn.Verifier
	sms    sms.Sender
	opt    Options
	tokens [][]byte
	log    *slog.Logger
	now    func() time.Time
}

func New(st *store.Store, rdb *redis.Client, ver *authn.Verifier, sender sms.Sender, opt Options, log *slog.Logger) *Server {
	s := &Server{st: st, rdb: rdb, ver: ver, sms: sender, opt: opt, log: log, now: time.Now}
	for _, t := range opt.InternalTokens {
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
	mux.HandleFunc("GET /api/v1/notifications", s.user(s.list))
	mux.HandleFunc("PUT /api/v1/notifications/{id}/read", s.user(s.markRead))
	mux.HandleFunc("GET /ws", s.handle(s.ws))
	mux.HandleFunc("POST /internal/notification/sms", s.internal(s.sendSMS))
	mux.HandleFunc("POST /internal/notification/device", s.internal(s.registerDevice))
	mux.HandleFunc("DELETE /internal/notification/device", s.internal(s.removeDevice))
	mux.HandleFunc("/", s.handle(func(http.ResponseWriter, *http.Request) error { return apperr.NotFound() }))
	return s.recoverer(s.withRequestID(s.accessLog(mux)))
}

type handlerFunc func(http.ResponseWriter, *http.Request) error

func (s *Server) handle(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeError(r.Context(), w, s.log, err)
		}
	}
}

// user — Bearer access token (client/driver; Identity public kaliti + Redis blacklist).
func (s *Server) user(h func(http.ResponseWriter, *http.Request, *authn.Identity) error) http.HandlerFunc {
	return s.handle(func(w http.ResponseWriter, r *http.Request) error {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(raw) == "" {
			return apperr.Unauthorized()
		}
		id, err := s.verify(r.Context(), strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		return h(w, r, id)
	})
}

func (s *Server) verify(ctx context.Context, raw string) (*authn.Identity, error) {
	id, err := s.ver.Verify(ctx, raw)
	if errors.Is(err, authn.ErrInvalid) {
		return nil, apperr.Unauthorized()
	}
	return id, err
}

func (s *Server) internal(h handlerFunc) http.HandlerFunc {
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

// ---------------------------------------------------------------- foydalanuvchi API

type notificationResp struct {
	ID        uuid.UUID         `json:"id"`
	Kind      string            `json:"kind"`
	TitleKey  string            `json:"title_key"`
	BodyKey   string            `json:"body_key,omitempty"`
	Params    map[string]string `json:"params"`
	Read      bool              `json:"read"`
	CreatedAt time.Time         `json:"created_at"`
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	q := r.URL.Query()
	limit := 20
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return apperr.Validation(map[string]string{"limit": "out_of_range"})
		}
		limit = n
	}
	cursor := q.Get("cursor")
	if len(cursor) > 512 {
		return apperr.Validation(map[string]string{"cursor": "invalid"})
	}
	items, err := s.st.List(r.Context(), id.UserID, cursor, limit)
	if errors.Is(err, store.ErrBadCursor) {
		return apperr.Validation(map[string]string{"cursor": "invalid"})
	}
	if err != nil {
		return err
	}
	out := struct {
		Data   []notificationResp `json:"data"`
		Cursor *string            `json:"cursor"`
	}{Data: make([]notificationResp, 0, len(items))}
	if len(items) > limit {
		items = items[:limit]
		c := store.EncodeCursor(items[limit-1])
		out.Cursor = &c
	}
	for _, n := range items {
		out.Data = append(out.Data, notificationResp(n))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) markRead(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	nid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.NotFound()
	}
	switch err := s.st.MarkRead(r.Context(), id.UserID, nid); {
	case errors.Is(err, store.ErrNotFound):
		return apperr.New(http.StatusNotFound, "NOT_FOUND", map[string]any{"id": nid.String()})
	case err != nil:
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------- ichki API

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) && te.Field != "" {
			return apperr.Validation(map[string]string{te.Field: "invalid_type"})
		}
		if m := err.Error(); strings.HasPrefix(m, "json: unknown field ") {
			return apperr.Validation(map[string]string{strings.Trim(strings.TrimPrefix(m, "json: unknown field "), `"`): "unknown_field"})
		}
		return apperr.Validation(map[string]string{"body": "invalid_json"})
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return apperr.Validation(map[string]string{"body": "invalid_json"})
	}
	return nil
}

var phoneRe = regexp.MustCompile(`^\+998[0-9]{9}$`)

type smsDTO struct {
	Phone       string            `json:"phone"`
	TemplateKey string            `json:"template_key"`
	Params      map[string]string `json:"params"`
}

func (s *Server) sendSMS(w http.ResponseWriter, r *http.Request) error {
	var in smsDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	f := map[string]string{}
	if !phoneRe.MatchString(in.Phone) {
		f["phone"] = "invalid_format"
	}
	text, err := sms.Render(in.TemplateKey, in.Params)
	switch {
	case errors.Is(err, sms.ErrUnknownTemplate):
		f["template_key"] = "unknown_template"
	case err != nil:
		f["params"] = "missing_or_invalid"
	}
	if len(f) > 0 {
		return apperr.Validation(f)
	}
	if err := s.sms.Send(r.Context(), in.Phone, text); err != nil {
		return fmt.Errorf("sms: %w", err)
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

type deviceDTO struct {
	UserID   string `json:"user_id"`
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

func (d *deviceDTO) validate(needPlatform bool) (uuid.UUID, map[string]string) {
	f := map[string]string{}
	uid, err := uuid.Parse(d.UserID)
	if err != nil {
		f["user_id"] = "invalid_format"
	}
	if d.Token == "" || len(d.Token) > 4096 {
		f["token"] = "invalid_length"
	}
	if needPlatform && d.Platform != "fcm" && d.Platform != "apns" {
		f["platform"] = "invalid_value"
	}
	return uid, f
}

func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request) error {
	var in deviceDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	uid, f := in.validate(true)
	if len(f) > 0 {
		return apperr.Validation(f)
	}
	switch err := s.st.RegisterDevice(r.Context(), uid, store.Device{Token: in.Token, Platform: in.Platform}); {
	case errors.Is(err, store.ErrTooManyDevices):
		return apperr.Validation(map[string]string{"token": "too_many_devices"})
	case err != nil:
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) removeDevice(w http.ResponseWriter, r *http.Request) error {
	var in deviceDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	uid, f := in.validate(false)
	if len(f) > 0 {
		return apperr.Validation(f)
	}
	if err := s.st.RemoveDevice(r.Context(), uid, in.Token); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
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
	if ae.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
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

// Unwrap — WebSocket (Hijacker) uchun kerak.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" {
			return
		}
		// Faqat yo'l loglanadi: /ws?token=... dagi JWT query'si loglarga tushmaydi.
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
