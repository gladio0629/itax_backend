package httpapi_test

import (
	"testing"

	"github.com/izgo/identity/internal/auth"
	"github.com/izgo/identity/internal/config"
	"github.com/izgo/shared/roles"
)

const (
	devCode   = "424242"
	devPhone  = "+998900000001"
	devEmail  = "qa@izgo.uz"
	devStaffP = "+998900000002"
)

// devEnv — config.Config.DevOTP() orqali (main.go dagi kabi) dev rejimli muhit.
func devEnv(t *testing.T, devMode bool) *env {
	cfg := config.Config{
		DevMode: devMode, DevOTPCode: devCode,
		DevTestPhones: []string{" " + devPhone, devStaffP, ""},
		DevTestEmails: []string{" QA@izgo.uz "},
	}
	return newEnv(t, func(o *auth.Options) { o.Dev = cfg.DevOTP() })
}

func otpVerify(e *env, phone, code string) resp {
	return e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phone, "code": code}, nil)
}

func TestDevOTPAllowlistedPhone(t *testing.T) {
	e := devEnv(t, true)
	if r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": devPhone}, nil); r.Status != 202 || r.Body["code_length"] != float64(6) {
		t.Fatalf("otp/request: %d %v", r.Status, r.Body)
	}
	if r := otpVerify(e, devPhone, devCode); r.Status != 200 || r.str("access_token") == "" {
		t.Fatalf("dev kod bilan verify: %d %v", r.Status, r.Body)
	}
	// boshqa qoidalar o'zgarmaydi: cooldown amal qiladi
	if r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": devPhone}, nil); r.Status != 429 {
		t.Fatalf("cooldown: %d %v", r.Status, r.Body)
	}
	// kod bir martalik
	if r := otpVerify(e, devPhone, devCode); r.Status != 401 || r.errKey() != "AUTH_OTP_EXPIRED" {
		t.Fatalf("dev kod qayta ishlatildi: %d %v", r.Status, r.Body)
	}
}

func TestDevOTPAttemptsUnchanged(t *testing.T) {
	e := devEnv(t, true)
	e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": devPhone}, nil)
	for _, left := range []float64{2, 1, 0} {
		if r := otpVerify(e, devPhone, "000000"); r.Status != 401 || r.errParam("attempts_left") != left {
			t.Fatalf("left=%v: %d %v", left, r.Status, r.Body)
		}
	}
	if r := otpVerify(e, devPhone, devCode); r.Status != 401 || r.errKey() != "AUTH_OTP_EXPIRED" {
		t.Fatalf("urinishlar tugagach dev kod: %d %v", r.Status, r.Body)
	}
}

// ro'yxatda yo'q telefon (dev rejimda ham) va DEV_MODE=false (ro'yxat e'tiborsiz) — tasodifiy kod.
func TestDevOTPNotApplied(t *testing.T) {
	for name, tc := range map[string]struct {
		devMode bool
		phone   string
	}{
		"dev rejim, ro'yxatda yo'q": {true, "+998901234567"},
		"DEV_MODE=false":            {false, devPhone},
	} {
		t.Run(name, func(t *testing.T) {
			e := devEnv(t, tc.devMode)
			if r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": tc.phone}, nil); r.Status != 202 {
				t.Fatalf("otp/request: %d", r.Status)
			}
			real := e.n.smsCode(tc.phone)
			if real == devCode {
				t.Skip("tasodifiy kod dev kodga teng chiqdi (1e-6)")
			}
			if r := otpVerify(e, tc.phone, devCode); r.Status != 401 || r.errKey() != "AUTH_OTP_INVALID" {
				t.Fatalf("dev kod qabul qilindi: %d %v", r.Status, r.Body)
			}
			if r := otpVerify(e, tc.phone, real); r.Status != 200 {
				t.Fatalf("haqiqiy kod: %d %v", r.Status, r.Body)
			}
		})
	}
}

func TestDevOTPAdminFlows(t *testing.T) {
	e := devEnv(t, true)
	e.addStaff(devEmail, devStaffP, roles.Manager, true)
	e.addStaff("", devPhone, roles.Operator, true)

	// email (katta harf bilan ham) -> dev kod, SMS -> dev kod
	r := adminStep1(e, "QA@IZGO.uz")
	cid := r.str("challenge_id")
	if r = e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": cid, "email_code": devCode, "phone": devStaffP}, nil); r.Status != 202 {
		t.Fatalf("email dev kodi: %d %v", r.Status, r.Body)
	}
	if r = e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": cid, "sms_code": devCode}, nil); r.Status != 200 {
		t.Fatalf("sms dev kodi: %d %v", r.Status, r.Body)
	}

	// email'siz operator: telefon -> dev kod
	r = e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"phone": devPhone}, nil)
	if r = e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": r.str("challenge_id"), "sms_code": devCode}, nil); r.Status != 200 {
		t.Fatalf("operator dev kodi: %d %v", r.Status, r.Body)
	}

	// ro'yxatda yo'q admin email'i — tasodifiy kod
	e.addStaff("boss@izgo.uz", "+998901112233", roles.Manager, true)
	r = adminStep1(e, "boss@izgo.uz")
	if e.n.emailCode("boss@izgo.uz") == devCode {
		t.Skip("tasodifiy kod dev kodga teng chiqdi")
	}
	if w := e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": r.str("challenge_id"), "email_code": devCode, "phone": "+998901112233"}, nil); w.Status != 401 {
		t.Fatalf("ro'yxatsiz email dev kod bilan o'tdi: %d %v", w.Status, w.Body)
	}
}
