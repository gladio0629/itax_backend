package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/izgo/notification/internal/apperr"
	"github.com/izgo/notification/internal/authn"
	"github.com/izgo/notification/internal/store"
	"github.com/redis/go-redis/v9"
)

// Yopish kodlari (3000–4999 ilova oralig'i).
const (
	CloseTokenExpired = websocket.StatusCode(4401) // token muddati tugadi, sessiya bekor qilindi yoki auth xabari noto'g'ri (F-19)
	CloseConnLimit    = websocket.StatusCode(4403) // foydalanuvchi ulanishlari limiti (faqat auth xabari rejimida; aks holda HTTP 403)
)

const (
	// WSSubprotocol — server faqat shu subprotokolni qaytaradi (bearer.<token> hech qachon qaytarilmaydi).
	WSSubprotocol = "izgo.v1"
	bearerPrefix  = "bearer."
	// authFrameLimit — auth xabari uchun o'qish chegarasi (JWT ~1 KB).
	authFrameLimit = 8 << 10
)

// ws — GET /ws. Token (Identity access token, client/driver) uch usulda keladi, ustuvorlik tartibida:
//
//  1. Subprotokol (tavsiya): new WebSocket(url, ["izgo.v1", "bearer.<token>"]) → Sec-WebSocket-Protocol sarlavhasi.
//     Token ulanishdan OLDIN tekshiriladi (noto'g'ri — HTTP 401, limit — HTTP 403). Javobda faqat "izgo.v1".
//     bearer.* "izgo.v1" siz, bir nechta bearer.* yoki bo'sh token — 401 (bearer'ni qaytarib bo'lmaydi, brauzer
//     tanlanmagan subprotokolli ulanishni baribir uzadi).
//  2. Birinchi xabar: token sarlavhada ham, query'da ham yo'q bo'lsa ulanish ochiladi va WS_AUTH_TIMEOUT ichida
//     bitta matn xabari {"type":"auth","token":"..."} kutiladi; muvaffaqiyatda server {"type":"auth.ok"} yuboradi.
//  3. Eski ?token= — faqat WS_ALLOW_QUERY_TOKEN=true bo'lsa (token Kong loglariga tushadi); false bo'lsa 401.
//     Subprotokolda token bo'lsa query e'tiborsiz qoldiriladi. Bo'sh ?token= — token yo'q deb olinadi.
//
// F-19: foydalanuvchi boshiga ulanishlar limiti (default 3); faqat o'z kanalidagi xabarlar (Redis ws:user:{id}); token
// muddati tugaganda yoki Identity sessiyani bekor qilganda (logout) ulanish uziladi; autentifikatsiyadan keyin klient
// yuborgan ma'lumot xabarlari ulanishni 1008 bilan yopadi.
//
// Token hech qachon loglanmaydi (accessLog faqat yo'lni yozadi).
func (s *Server) ws(w http.ResponseWriter, r *http.Request) error {
	raw, err := s.wsToken(r)
	if err != nil {
		return err
	}
	if raw == "" {
		s.wsFirstMessage(w, r)
		return nil
	}
	id, err := s.verify(r.Context(), raw)
	if err != nil {
		return err
	}
	sess, err := s.openSession(r.Context(), id)
	if err != nil {
		return err
	}
	defer sess.close()
	c, err := websocket.Accept(w, r, s.acceptOptions())
	if err != nil {
		return nil // Accept javobni o'zi yozgan (masalan, Origin ruxsat etilmagan — 403)
	}
	ctx := c.CloseRead(r.Context()) // klient ma'lumot yuborsa 1008 bilan yopiladi; ctx ulanish yopilganda bekor bo'ladi
	s.pump(ctx, c, sess)
	return nil
}

func (s *Server) acceptOptions() *websocket.AcceptOptions {
	return &websocket.AcceptOptions{Subprotocols: []string{WSSubprotocol}, OriginPatterns: s.opt.WSAllowedOrigins}
}

// wsToken — so'rovdan tokenni oladi (subprotokol > query). "" — token yo'q (birinchi xabar rejimi).
func (s *Server) wsToken(r *http.Request) (string, error) {
	var (
		hasProto bool
		bearers  []string
	)
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			switch {
			case strings.EqualFold(p, WSSubprotocol):
				hasProto = true
			case strings.HasPrefix(p, bearerPrefix):
				bearers = append(bearers, strings.TrimPrefix(p, bearerPrefix))
			}
		}
	}
	if len(bearers) > 0 {
		if !hasProto || len(bearers) != 1 || bearers[0] == "" {
			return "", apperr.Unauthorized()
		}
		return bearers[0], nil
	}
	if q := r.URL.Query().Get("token"); q != "" {
		if !s.opt.WSAllowQueryToken {
			return "", apperr.Unauthorized()
		}
		// Token loglanmaydi — faqat hisoblagich.
		n := s.queryTokenUses.Add(1)
		s.log.InfoContext(r.Context(), "ws: eskirgan ?token= ishlatildi (Sec-WebSocket-Protocol yoki auth xabariga o'tish kerak)",
			"legacy_query_token_total", n, "request_id", requestID(r.Context()))
		return q, nil
	}
	return "", nil
}

// wsSession — tasdiqlangan ulanish: slot (Redis) + foydalanuvchi kanaliga obuna.
type wsSession struct {
	s      *Server
	id     *authn.Identity
	connID uuid.UUID
	sub    *redis.PubSub
}

// close — obunani yopadi va slotni bo'shatadi.
func (ss *wsSession) close() {
	_ = ss.sub.Close()
	_ = ss.s.st.ReleaseConn(context.Background(), ss.id.UserID, ss.connID)
}

// refresh — slot muddatini yangilaydi (ping muvaffaqiyatli bo'lganda).
func (ss *wsSession) refresh(ctx context.Context) {
	_ = ss.s.st.RefreshConn(ctx, ss.id.UserID, ss.connID, ss.s.now())
}

// openSession — ulanish slotini oladi va foydalanuvchi kanaliga obuna bo'ladi. Obuna tasdig'i kelgandan KEYIN
// ulanish ochiladi/auth.ok yuboriladi: shundan so'ng Publish soni bu ulanishni hisoblaydi (hech kim yo'q bo'lsa
// servis push'ga o'tadi). Limit to'lgan bo'lsa apperr FORBIDDEN (ws_connection_limit).
func (s *Server) openSession(ctx context.Context, id *authn.Identity) (*wsSession, error) {
	connID := uuid.New()
	ok, err := s.st.AcquireConn(ctx, id.UserID, connID, s.opt.WSMaxConns, s.now())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, apperr.Forbidden("ws_connection_limit")
	}
	ss := &wsSession{s: s, id: id, connID: connID, sub: s.rdb.Subscribe(ctx, store.WSChannel(id.UserID))}
	if _, err := ss.sub.Receive(ctx); err != nil {
		ss.close()
		return nil, err
	}
	return ss, nil
}

type authFrame struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

var authOK = []byte(`{"type":"auth.ok"}`)

// wsFirstMessage — token so'rovda yo'q: ulanish ochiladi va bitta auth xabari kutiladi.
// Yopish kodlari: 4401 auth_timeout (vaqt tugadi), 1008 auth_malformed (binar/JSON emas/type != auth/token bo'sh),
// 4401 token_invalid (noto'g'ri, muddati o'tgan yoki bekor qilingan token), 4403 ws_connection_limit, 1011 internal_error.
func (s *Server) wsFirstMessage(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, s.acceptOptions())
	if err != nil {
		return // Accept javobni o'zi yozgan
	}
	defer func() { _ = c.CloseNow() }() // allaqachon yopilgan bo'lsa hech narsa qilmaydi
	ctx := r.Context()

	id := s.readAuth(ctx, c)
	if id == nil {
		return
	}
	sess, err := s.openSession(ctx, id)
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Status == http.StatusForbidden {
			_ = c.Close(CloseConnLimit, "ws_connection_limit")
			return
		}
		s.log.ErrorContext(ctx, "ws: sessiya ochilmadi", "err", err, "request_id", requestID(ctx))
		_ = c.Close(websocket.StatusInternalError, "internal_error")
		return
	}
	defer sess.close()

	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = c.Write(wctx, websocket.MessageText, authOK)
	cancel()
	if err != nil {
		return
	}
	pctx := c.CloseRead(ctx) // bundan keyin klient ma'lumot yuborsa 1008
	s.pump(pctx, c, sess)
}

// readAuth — bitta auth xabarini o'qiydi va tokenni tekshiradi. nil bo'lsa ulanish allaqachon yopilgan/yopilmoqda.
// Vaqt tugashi uchun Read kontekstiga deadline qo'yilmaydi: kutubxona bunda ulanishni close-frame'siz uzadi,
// shuning uchun taymer 4401 auth_timeout bilan yopadi.
func (s *Server) readAuth(ctx context.Context, c *websocket.Conn) *authn.Identity {
	c.SetReadLimit(authFrameLimit)
	var timedOut atomic.Bool
	t := time.AfterFunc(s.opt.WSAuthTimeout, func() {
		timedOut.Store(true)
		_ = c.Close(CloseTokenExpired, "auth_timeout")
	})
	typ, data, err := c.Read(ctx)
	if !t.Stop() || timedOut.Load() {
		return nil // taymer yopmoqda
	}
	if err != nil {
		return nil // klient uzildi yoki o'qish chegarasi oshdi (kutubxona yopadi)
	}
	var f authFrame
	if typ != websocket.MessageText || json.Unmarshal(data, &f) != nil || f.Type != "auth" || strings.TrimSpace(f.Token) == "" {
		_ = c.Close(websocket.StatusPolicyViolation, "auth_malformed")
		return nil
	}
	id, err := s.ver.Verify(ctx, strings.TrimSpace(f.Token))
	if errors.Is(err, authn.ErrInvalid) {
		_ = c.Close(CloseTokenExpired, "token_invalid")
		return nil
	}
	if err != nil {
		s.log.ErrorContext(ctx, "ws: token tekshiruvi", "err", err, "request_id", requestID(ctx))
		_ = c.Close(websocket.StatusInternalError, "internal_error")
		return nil
	}
	return id
}

func (s *Server) pump(ctx context.Context, c *websocket.Conn, sess *wsSession) {
	id, msgs := sess.id, sess.sub.Channel()
	var expiry <-chan time.Time
	if !id.Expires.IsZero() {
		t := time.NewTimer(time.Until(id.Expires))
		defer t.Stop()
		expiry = t.C
	}
	ping := time.NewTicker(s.opt.WSPingInterval)
	defer ping.Stop()
	revoke := time.NewTicker(s.opt.WSRevokeCheck)
	defer revoke.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = c.CloseNow()
			return
		case <-expiry:
			_ = c.Close(CloseTokenExpired, "token_expired")
			return
		case <-revoke.C:
			if revoked, err := s.ver.Revoked(ctx, id.SID); err == nil && revoked {
				_ = c.Close(CloseTokenExpired, "session_revoked")
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				_ = c.CloseNow()
				return
			}
			sess.refresh(ctx)
		case m, ok := <-msgs:
			if !ok {
				_ = c.Close(websocket.StatusGoingAway, "server_shutdown")
				return
			}
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.Write(wctx, websocket.MessageText, []byte(m.Payload))
			cancel()
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					s.log.Debug("ws yozib bo'lmadi", "err", err)
				}
				_ = c.CloseNow()
				return
			}
		}
	}
}
