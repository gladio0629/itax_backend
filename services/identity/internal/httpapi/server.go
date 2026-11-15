// Package httpapi — identity servisining HTTP qatlami (net/http, Go 1.22 marshrutlash).
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/identity/internal/apperr"
	"github.com/izgo/identity/internal/auth"
	"github.com/izgo/identity/internal/token"
	"github.com/izgo/shared/roles"
)

// InternalTokenHeader — ichki servislararo chaqiruv siri (pricing bilan bir xil sxema).
const InternalTokenHeader = "X-Internal-Token"

type Server struct {
	svc    *auth.Service
	tokens [][]byte // ichki API tokenlari (INTERNAL_TOKENS)
	log    *slog.Logger
}

func New(svc *auth.Service, internalTokens []string, log *slog.Logger) *Server {
	s := &Server{svc: svc, log: log}
	for _, t := range internalTokens {
		if t = strings.TrimSpace(t); t != "" {
			s.tokens = append(s.tokens, []byte(t))
		}
	}
	return s
}

type ctxKey int

const (
	keyRequestID ctxKey = iota
	keyClaims
)

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

// authed — Bearer access token (client/driver) talab qiladi; claims kontekstga qo'yiladi.
func (s *Server) authed(h func(w http.ResponseWriter, r *http.Request, c *token.Claims) error) http.HandlerFunc {
	return s.handle(func(w http.ResponseWriter, r *http.Request) error {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			return apperr.Unauthorized()
		}
		c, err := s.svc.Authenticate(r.Context(), strings.TrimSpace(raw))
		if err != nil {
			return err
		}
		return h(w, r, c)
	})
}

// internal — /internal/* uchun X-Internal-Token tekshiruvi. Vaqt bo'yicha o'zgarmas taqqoslash, barcha
// tokenlar tekshiriladi; token sozlanmagan bo'lsa hammasi rad etiladi (fail closed).
func (s *Server) internal(h handlerFunc) http.HandlerFunc {
	return s.handle(func(w http.ResponseWriter, r *http.Request) error {
		got := []byte(r.Header.Get(InternalTokenHeader))
		ok := 0
		for _, t := range s.tokens {
			ok |= subtle.ConstantTimeCompare(got, t)
		}
		if ok != 1 || len(got) == 0 {
			return apperr.ForbiddenReason("internal_only")
		}
		return h(w, r)
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("POST /api/v1/auth/otp/request", s.handle(s.otpRequest))
	mux.HandleFunc("POST /api/v1/auth/otp/verify", s.handle(s.otpVerify))
	mux.HandleFunc("POST /api/v1/auth/token/refresh", s.handle(s.tokenRefresh))
	mux.HandleFunc("POST /api/v1/auth/logout", s.authed(s.logout))

	mux.HandleFunc("POST /admin/v1/auth/login/email", s.handle(s.adminLoginEmail))
	mux.HandleFunc("POST /admin/v1/auth/login/sms", s.handle(s.adminLoginSMS))
	mux.HandleFunc("POST /admin/v1/auth/login/verify", s.handle(s.adminLoginVerify))

	// Ichki API: Kong bu yo'lni tashqariga chiqarmaydi; qo'shimcha ravishda X-Internal-Token talab qilinadi.
	mux.HandleFunc("POST /internal/identity/staff", s.internal(s.internalStaffCreate))
	mux.HandleFunc("PUT /internal/identity/staff/{id}", s.internal(s.internalStaffUpdate))
	mux.HandleFunc("GET /internal/identity/users/{id}", s.internal(s.internalUserGet))

	mux.HandleFunc("GET /api/v1/profile", s.authed(s.profileGet))
	mux.HandleFunc("PUT /api/v1/profile", s.authed(s.profileUpdate))
	mux.HandleFunc("PUT /api/v1/profile/settings", s.authed(s.settingsUpdate))

	mux.HandleFunc("/", s.handle(func(http.ResponseWriter, *http.Request) error { return apperr.NotFound() }))

	return s.recoverer(s.withRequestID(s.accessLog(mux)))
}

// ---- handlers

func (s *Server) otpRequest(w http.ResponseWriter, r *http.Request) error {
	var in otpRequestDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	res, err := s.svc.RequestOTP(r.Context(), in.Phone, clientIP(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, otpRequestResp{ExpiresIn: res.ExpiresIn, ResendAfter: res.ResendAfter})
	return nil
}

func (s *Server) otpVerify(w http.ResponseWriter, r *http.Request) error {
	var in otpVerifyDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	app, ok := auth.ParseApp(r.Header.Get("X-App"))
	if !ok {
		return apperr.Validation(map[string]string{"x-app": "invalid_value"})
	}
	pair, err := s.svc.VerifyOTP(r.Context(), in.Phone, in.Code, app, requestID(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newTokenPairResp(pair))
	return nil
}

func (s *Server) tokenRefresh(w http.ResponseWriter, r *http.Request) error {
	var in refreshDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	pair, err := s.svc.Refresh(r.Context(), in.RefreshToken)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newTokenPairResp(pair))
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, c *token.Claims) error {
	var in refreshDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := s.svc.Logout(r.Context(), c, in.RefreshToken); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) adminLoginEmail(w http.ResponseWriter, r *http.Request) error {
	var in adminEmailDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	ch, err := s.svc.AdminLoginEmail(r.Context(), in.Email, clientIP(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, challengeResp{ChallengeID: ch.ID, ExpiresIn: ch.ExpiresIn})
	return nil
}

func (s *Server) adminLoginSMS(w http.ResponseWriter, r *http.Request) error {
	var in adminSMSDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	var (
		ch  auth.Challenge
		err error
	)
	if in.phoneOnly() {
		// Email'siz operator: telefon -> SMS (email qadami yo'q)
		ch, err = s.svc.AdminLoginPhone(r.Context(), in.Phone, clientIP(r))
	} else {
		cid, _ := uuid.Parse(in.ChallengeID) // validate() allaqachon tekshirgan
		ch, err = s.svc.AdminLoginSMS(r.Context(), cid, in.EmailCode, in.Phone)
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusAccepted, challengeResp{ChallengeID: ch.ID, ExpiresIn: ch.ExpiresIn})
	return nil
}

func (s *Server) adminLoginVerify(w http.ResponseWriter, r *http.Request) error {
	var in adminVerifyDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	cid, _ := uuid.Parse(in.ChallengeID)
	t, err := s.svc.AdminLoginVerify(r.Context(), cid, in.SMSCode)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, adminTokenResp{AccessToken: t.AccessToken, ExpiresIn: t.ExpiresIn, TokenType: "Bearer"})
	return nil
}

func (s *Server) internalStaffCreate(w http.ResponseWriter, r *http.Request) error {
	var in internalStaffDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	st, err := s.svc.CreateStaff(r.Context(), auth.NewStaff{
		Phone: in.Phone, Email: in.Email, Role: roles.Role(in.Role), Name: strings.TrimSpace(in.Name),
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, internalStaffResp{ID: st.ID, Phone: st.Phone, Role: string(st.Role)})
	return nil
}

func (s *Server) internalStaffUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation(map[string]string{"id": "invalid_format"})
	}
	var in internalStaffUpdateDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	st, err := s.svc.UpdateStaff(r.Context(), id, in.patch())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newInternalStaffFullResp(st))
	return nil
}

func (s *Server) internalUserGet(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation(map[string]string{"id": "invalid_format"})
	}
	u, err := s.svc.LookupUser(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, internalUserResp{ID: u.ID, Phone: u.Phone, FirstName: u.FirstName, LastName: u.LastName, Role: string(u.Role)})
	return nil
}

func userID(c *token.Claims) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Subject)
	if err != nil {
		return uuid.Nil, apperr.Unauthorized()
	}
	return id, nil
}

func (s *Server) profileGet(w http.ResponseWriter, r *http.Request, c *token.Claims) error {
	id, err := userID(c)
	if err != nil {
		return err
	}
	u, err := s.svc.GetProfile(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newProfileResp(u))
	return nil
}

func (s *Server) profileUpdate(w http.ResponseWriter, r *http.Request, c *token.Claims) error {
	id, err := userID(c)
	if err != nil {
		return err
	}
	var in profileUpdateDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	u, err := s.svc.UpdateProfile(r.Context(), id, in.FirstName, in.LastName)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newProfileResp(u))
	return nil
}

func (s *Server) settingsUpdate(w http.ResponseWriter, r *http.Request, c *token.Claims) error {
	id, err := userID(c)
	if err != nil {
		return err
	}
	var in settingsDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	st, err := s.svc.UpdateSettings(r.Context(), id, in.update())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newSettingsResp(st))
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
		// Tana va tokenlar logga yozilmaydi.
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

// clientIP — Kong ortida RemoteAddr Kong'ning o'zi bo'ladi, shuning uchun Kong qo'yadigan
// X-Real-IP, bo'lmasa X-Forwarded-For ning o'ng tomondagi yozuvi ishlatiladi. Servis tashqariga ochiq emas (faqat Kong orqali),
// shuning uchun bu sarlavhalarga ishoniladi.
func clientIP(r *http.Request) string {
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	// XFF'da chap tomon mijoz yuborgan (soxta bo'lishi mumkin); o'ng tomondagisini proksi (Kong) qo'shadi.
	if parts := strings.Split(r.Header.Get("X-Forwarded-For"), ","); len(parts) > 0 {
		if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
