package httpapi_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestInternalTokenRequired(t *testing.T) {
	e := newEnv(t)
	for _, ep := range [][2]string{{"POST", "/internal/notification/sms"}, {"POST", "/internal/notification/device"}, {"DELETE", "/internal/notification/device"}} {
		for name, h := range map[string]map[string]string{
			"yo'q": nil, "noto'g'ri": {"X-Internal-Token": "wrong-token-0123456789"}, "foydalanuvchi JWT": bearer(e.tok("driver", uuid.New())),
		} {
			if r := e.do(ep[0], ep[1], h, `{}`); r.Status != 403 || r.errKey() != "FORBIDDEN" {
				t.Errorf("%s %s [%s]: %d %s", ep[0], ep[1], name, r.Status, r.Raw)
			}
		}
	}
}

func TestSMSMock(t *testing.T) {
	e := newEnv(t, func(o *opts) { o.logSMSText = false })
	body := map[string]any{"phone": "+998901234567", "template_key": "otp", "params": map[string]string{"code": "482913"}}
	if r := e.do("POST", "/internal/notification/sms", internal(), body); r.Status != 202 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "SMS (mock") || !strings.Contains(logs, "+998*****67") {
		t.Errorf("log: %s", logs)
	}
	if strings.Contains(logs, "482913") || strings.Contains(logs, "+998901234567") {
		t.Errorf("OTP kodi yoki to'liq telefon logda: %s", logs)
	}
	bad := map[string]any{
		"telefon":         map[string]any{"phone": "901234567", "template_key": "otp", "params": map[string]string{"code": "1"}},
		"shablon":         map[string]any{"phone": "+998901234567", "template_key": "nope", "params": map[string]string{"code": "1"}},
		"param yo'q":      map[string]any{"phone": "+998901234567", "template_key": "otp", "params": map[string]string{}},
		"noma'lum maydon": map[string]any{"phone": "+998901234567", "template_key": "otp", "params": map[string]string{"code": "1"}, "x": 1},
		"camelCase kalit": map[string]any{"phone": "+998901234567", "templateKey": "otp", "params": map[string]string{"code": "1"}},
	}
	for name, b := range bad {
		if r := e.do("POST", "/internal/notification/sms", internal(), b); r.Status != 422 || r.errKey() != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
}

func TestSMSMockLogsTextOnlyWhenEnabled(t *testing.T) {
	e := newEnv(t, func(o *opts) { o.logSMSText = true })
	e.do("POST", "/internal/notification/sms", internal(), map[string]any{"phone": "+998901234567", "template_key": "otp", "params": map[string]string{"code": "482913"}})
	if !strings.Contains(e.logs.String(), "482913") {
		t.Errorf("dev rejimida kod ko'rinishi kerak: %s", e.logs.String())
	}
}

func TestDeviceRegistry(t *testing.T) {
	e := newEnv(t)
	a, b := uuid.New(), uuid.New()
	e.device(a, "token-aaaaaaaaaaaa-1", "fcm")
	e.device(a, "token-aaaaaaaaaaaa-2", "apns")
	e.device(a, "token-aaaaaaaaaaaa-2", "apns") // takroriy ro'yxatdan o'tish zarar qilmaydi
	if got := e.mr.HGet("user:"+a.String()+":devices", "token-aaaaaaaaaaaa-1"); got != "fcm" {
		t.Errorf("user:{id}:devices: %q", got)
	}
	if devs, _ := e.st.Devices(ctx, a); len(devs) != 2 {
		t.Fatalf("%+v", devs)
	}
	// qurilma boshqa foydalanuvchiga o'tsa (logout/login) eski egasi push olmaydi
	e.device(b, "token-aaaaaaaaaaaa-1", "fcm")
	if devs, _ := e.st.Devices(ctx, a); len(devs) != 1 || devs[0].Token != "token-aaaaaaaaaaaa-2" {
		t.Errorf("eski egasida qoldi: %+v", devs)
	}
	_ = e.svc.Handle(ctx, e.orderCreated(a, uuid.New()))
	for _, p := range e.cap.pushed() {
		if p.Token == "token-aaaaaaaaaaaa-1" {
			t.Error("qurilma eski egasiga push oldi")
		}
	}
	// limit (testda 3)
	e.device(a, "token-aaaaaaaaaaaa-3", "fcm")
	e.device(a, "token-aaaaaaaaaaaa-4", "fcm") // 3-qurilma: limitga yetdi
	if r := e.do("POST", "/internal/notification/device", internal(), map[string]any{"user_id": a.String(), "token": "token-aaaaaaaaaaaa-5", "platform": "fcm"}); r.Status != 422 {
		t.Errorf("limit: %d %s", r.Status, r.Raw)
	}
	// o'chirish
	if r := e.do("DELETE", "/internal/notification/device", internal(), map[string]any{"user_id": a.String(), "token": "token-aaaaaaaaaaaa-3"}); r.Status != 204 {
		t.Errorf("delete: %d", r.Status)
	}
	if devs, _ := e.st.Devices(ctx, a); len(devs) != 2 {
		t.Errorf("%+v", devs)
	}
	for name, body := range map[string]any{
		"user_id": map[string]any{"user_id": "x", "token": "t", "platform": "fcm"}, "token": map[string]any{"user_id": a.String(), "token": "", "platform": "fcm"},
		"platform": map[string]any{"user_id": a.String(), "token": "t", "platform": "web"},
	} {
		if r := e.do("POST", "/internal/notification/device", internal(), body); r.Status != 422 {
			t.Errorf("%s: %d", name, r.Status)
		}
	}
}
