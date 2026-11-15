package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/izgo/notification/internal/apperr"
	"github.com/izgo/notification/internal/store"
	"github.com/redis/go-redis/v9"
	"net/http"
)

// Yopish kodlari (3000–4999 ilova oralig'i).
const (
	CloseTokenExpired = websocket.StatusCode(4401) // token muddati tugadi yoki sessiya bekor qilindi (F-19)
)

// ws — GET /ws?token=JWT
//
// F-19: token ulanishdan OLDIN tekshiriladi (noto'g'ri bo'lsa 401, ulanish ochilmaydi); foydalanuvchi boshiga ulanishlar
// limiti (default 3, ortig'i 403); faqat o'z kanalidagi xabarlar (Redis ws:user:{id}); token muddati tugaganda yoki
// Identity sessiyani bekor qilganda (logout) ulanish uziladi; klient yuborgan ma'lumot xabarlari ulanishni yopadi.
//
// Token query'da (brauzer/mobil WS API sarlavha qo'ya olmaydi): u loglarga tushmasligi uchun faqat yo'l loglanadi.
func (s *Server) ws(w http.ResponseWriter, r *http.Request) error {
	raw := r.URL.Query().Get("token")
	if raw == "" {
		return apperr.Unauthorized()
	}
	id, err := s.verify(r.Context(), raw)
	if err != nil {
		return err
	}
	connID := uuid.New()
	ok, err := s.st.AcquireConn(r.Context(), id.UserID, connID, s.opt.WSMaxConns, s.now())
	if err != nil {
		return err
	}
	if !ok {
		return apperr.Forbidden("ws_connection_limit")
	}
	release := func() { _ = s.st.ReleaseConn(context.Background(), id.UserID, connID) }

	// Kanalga obuna bo'lib, tasdiq kelgandan KEYIN ulanishni ochamiz: shundan so'ng Publish soni bu ulanishni hisoblaydi
	// (hech kim yo'q bo'lsa servis push'ga o'tadi).
	sub := s.rdb.Subscribe(r.Context(), store.WSChannel(id.UserID))
	if _, err := sub.Receive(r.Context()); err != nil {
		_ = sub.Close()
		release()
		return err
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		_ = sub.Close()
		release()
		return nil // Accept javobni o'zi yozgan
	}
	defer func() { _ = sub.Close(); release() }()

	ctx := c.CloseRead(r.Context()) // klient ma'lumot yuborsa 1008 bilan yopiladi; ctx ulanish yopilganda bekor bo'ladi
	s.pump(ctx, c, sub.Channel(), id.UserID, connID, id.SID, id.Expires)
	return nil
}

func (s *Server) pump(ctx context.Context, c *websocket.Conn, msgs <-chan *redis.Message, user, connID uuid.UUID, sid string, expires time.Time) {
	var expiry <-chan time.Time
	if !expires.IsZero() {
		t := time.NewTimer(time.Until(expires))
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
			if revoked, err := s.ver.Revoked(ctx, sid); err == nil && revoked {
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
			_ = s.st.RefreshConn(ctx, user, connID, s.now())
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
