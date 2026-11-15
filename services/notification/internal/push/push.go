// Package push — qurilmalarga push yuborish (FCM/APNs). HOZIR MOCK: logga yozadi.
package push

import (
	"context"
	"errors"
	"log/slog"
)

// ErrUnregistered — token yaroqsiz (ilova o'chirilgan): chaqiruvchi uni ro'yxatdan olib tashlaydi.
var ErrUnregistered = errors.New("push: device token unregistered")

// Pusher — bitta qurilmaga data-only push. data FAQAT {type, id} (F-21); tafsilot ilovada serverdan olinadi.
type Pusher interface {
	Send(ctx context.Context, platform, token string, data map[string]string) error
}

// Mock — haqiqiy FCM/APNs o'rniga log. Token to'liq yozilmaydi.
type Mock struct{ Log *slog.Logger }

func (m Mock) Send(ctx context.Context, platform, token string, data map[string]string) error {
	m.Log.InfoContext(ctx, "PUSH (mock, haqiqiy yuborilmaydi)", "platform", platform, "token", Mask(token), "data", data)
	return nil
}

// Mask — tokenning faqat boshi va oxiri.
func Mask(s string) string {
	if len(s) <= 10 {
		return "***"
	}
	return s[:4] + "…" + s[len(s)-4:]
}

/*
Haqiqiy FCM (HTTP v1) — Mock o'rniga. Kalitlar kodda emas: servis akkaunt JSON yo'li muhitdan (FCM_CREDENTIALS_FILE),
loyiha ID si FCM_PROJECT_ID. OAuth2 token: golang.org/x/oauth2/google, scope "https://www.googleapis.com/auth/firebase.messaging".

	type FCM struct {
		ProjectID string
		HTTP      *http.Client // oauth2 transport'li: google.DefaultClient(ctx, scope)
	}

	func (f FCM) Send(ctx context.Context, platform, token string, data map[string]string) error {
		body, _ := json.Marshal(map[string]any{"message": map[string]any{
			"token": token,
			"data":  data, // faqat data-only: sarlavha/matn yo'q (F-21)
			"android": map[string]any{"priority": "HIGH"},
			// APNs FCM orqali: ilova fonda uyg'onib, tafsilotni serverdan oladi
			"apns": map[string]any{
				"headers": map[string]string{"apns-priority": "5", "apns-push-type": "background"},
				"payload": map[string]any{"aps": map[string]any{"content-available": 1}},
			},
		}})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
			"https://fcm.googleapis.com/v1/projects/"+f.ProjectID+"/messages:send", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := f.HTTP.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusOK:
			return nil
		case resp.StatusCode == http.StatusNotFound: // UNREGISTERED
			return ErrUnregistered
		default:
			return fmt.Errorf("fcm: status %d", resp.StatusCode)
		}
	}

To'g'ridan-to'g'ri APNs (HTTP/2): POST https://api.push.apple.com/3/device/<token>, sarlavhalar: authorization: bearer <ES256 JWT
(kalit .p8 fayldan, kid + team id)>, apns-topic: <bundle id>, apns-push-type: background, apns-priority: 5;
body: {"aps":{"content-available":1},"type":"...","id":"..."}; 410 Gone -> ErrUnregistered.
*/
