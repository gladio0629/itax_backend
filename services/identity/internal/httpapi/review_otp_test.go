package httpapi_test

import (
	"testing"

	"github.com/izgo/identity/internal/auth"
	"github.com/izgo/shared/roles"
)

const (
	reviewCode  = "135790"
	reviewPhone = "+998900000077"
)

func reviewEnv(t *testing.T) *env {
	return newEnv(t, func(o *auth.Options) {
		o.Review = auth.ReviewOTP{Code: reviewCode, Phones: map[string]struct{}{reviewPhone: {}}}
	})
}

// Review telefoni: belgilangan kod bilan kiradi, SMS yuborilmaydi, boshqa qoidalar o'zgarmaydi.
func TestReviewOTPPhone(t *testing.T) {
	e := reviewEnv(t)
	if r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": reviewPhone}, nil); r.Status != 202 {
		t.Fatalf("otp/request: %d %v", r.Status, r.Body)
	}
	if got := e.n.smsCode(reviewPhone); got != "" {
		t.Fatalf("review telefoniga SMS yuborildi: %q", got)
	}
	if r := otpVerify(e, reviewPhone, reviewCode); r.Status != 200 || r.str("access_token") == "" {
		t.Fatalf("review kod bilan verify: %d %v", r.Status, r.Body)
	}
	if r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": reviewPhone}, nil); r.Status != 429 {
		t.Fatalf("cooldown: %d %v", r.Status, r.Body)
	}
	if r := otpVerify(e, reviewPhone, reviewCode); r.Status != 401 || r.errKey() != "AUTH_OTP_EXPIRED" {
		t.Fatalf("kod bir martalik emas: %d %v", r.Status, r.Body)
	}
}

func TestReviewOTPAttemptsUnchanged(t *testing.T) {
	e := reviewEnv(t)
	e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": reviewPhone}, nil)
	for _, left := range []float64{2, 1, 0} {
		if r := otpVerify(e, reviewPhone, "000000"); r.Status != 401 || r.errParam("attempts_left") != left {
			t.Fatalf("left=%v: %d %v", left, r.Status, r.Body)
		}
	}
	if r := otpVerify(e, reviewPhone, reviewCode); r.Status != 401 || r.errKey() != "AUTH_OTP_EXPIRED" {
		t.Fatalf("urinishlar tugagach review kod: %d %v", r.Status, r.Body)
	}
}

// Ro'yxatda yo'q telefon — odatdagidek tasodifiy kod va SMS.
func TestReviewOTPOtherPhoneUnaffected(t *testing.T) {
	e := reviewEnv(t)
	const phone = "+998901234567"
	if r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phone}, nil); r.Status != 202 {
		t.Fatalf("otp/request: %d", r.Status)
	}
	got := e.n.smsCode(phone)
	if len(got) != 6 {
		t.Fatalf("SMS yuborilmadi: %q", got)
	}
	if got == reviewCode {
		t.Skip("tasodifiy kod review kodga teng chiqdi (1e-6)")
	}
	if r := otpVerify(e, phone, reviewCode); r.Status != 401 {
		t.Fatalf("review kod begona telefonda ishladi: %d %v", r.Status, r.Body)
	}
}

// Review telefoni xodimga tegishli bo'lsa ham admin kirishida belgilangan kod ishlamaydi.
func TestReviewOTPNotForAdmin(t *testing.T) {
	e := reviewEnv(t)
	e.addStaff("", reviewPhone, roles.Operator, true)
	r := e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"phone": reviewPhone}, nil)
	if r.Status != 202 {
		t.Fatalf("admin login/sms: %d %v", r.Status, r.Body)
	}
	got := e.n.smsCode(reviewPhone)
	if len(got) != 6 {
		t.Fatalf("admin SMS yuborilmadi: %q", got)
	}
	if got == reviewCode {
		t.Skip("tasodifiy kod review kodga teng chiqdi (1e-6)")
	}
	if r = e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": r.str("challenge_id"), "sms_code": reviewCode}, nil); r.Status != 401 {
		t.Fatalf("review kod admin kirishida ishladi: %d %v", r.Status, r.Body)
	}
}
