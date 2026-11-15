package httpapi_test

import (
	"fmt"
	"testing"
	"time"
)

func TestOTPWrongAttemptsThenCodeDeleted(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, nil)
	good := e.n.smsCode(phoneA)
	bad := "000000"
	if good == bad {
		bad = "111111"
	}
	verify := func(code string) resp {
		return e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phoneA, "code": code}, nil)
	}

	for _, left := range []float64{2, 1, 0} {
		r := verify(bad)
		if r.Status != 401 || r.errKey() != "AUTH_OTP_INVALID" || r.errParam("attempts_left") != left {
			t.Fatalf("attempts_left=%v kutilgan: %d %v", left, r.Status, r.Body)
		}
	}
	// 3 noto'g'ri urinishdan keyin kod o'chgan: to'g'ri kod ham endi ishlamaydi
	if r := verify(good); r.Status != 401 || r.errKey() != "AUTH_OTP_EXPIRED" {
		t.Fatalf("to'g'ri kod o'chirilgan kodni tiklamasligi kerak: %d %v", r.Status, r.Body)
	}
}

func TestOTPCorrectCodeAfterSomeWrong(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, nil)
	good := e.n.smsCode(phoneA)
	bad := "000000"
	if good == bad {
		bad = "111111"
	}
	e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phoneA, "code": bad}, nil)
	r := e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phoneA, "code": good}, nil)
	if r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func TestOTPExpiresAfterThreeMinutes(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, nil)
	code := e.n.smsCode(phoneA)

	e.mr.FastForward(3*time.Minute - time.Second)
	// hali amal qiladi — lekin sinab ko'rish uchun yangi kod so'ramaymiz, faqat muddatni o'tkazamiz
	e.mr.FastForward(2 * time.Second)
	r := e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phoneA, "code": code}, nil)
	if r.Status != 401 || r.errKey() != "AUTH_OTP_EXPIRED" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func TestOTPStillValidJustBeforeExpiry(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, nil)
	code := e.n.smsCode(phoneA)
	e.mr.FastForward(3*time.Minute - time.Second)
	if r := e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phoneA, "code": code}, nil); r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func TestOTPResendCooldown(t *testing.T) {
	e := newEnv(t)
	if r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, nil); r.Status != 202 {
		t.Fatal(r.Status)
	}
	first := e.n.smsCode(phoneA)
	r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, nil)
	if r.Status != 429 || r.errKey() != "AUTH_OTP_RATE_LIMITED" || r.Header.Get("Retry-After") == "" {
		t.Fatalf("cooldown: %d %v", r.Status, r.Body)
	}
	if e.n.smsCode(phoneA) != first {
		t.Fatal("cooldown paytida yangi kod yuborildi")
	}
	e.mr.FastForward(61 * time.Second)
	if r = e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, nil); r.Status != 202 {
		t.Fatalf("cooldown tugagach: %d", r.Status)
	}
}

func TestOTPPhoneLimitFivePerHour(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 5; i++ {
		if r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, map[string]string{"X-Real-IP": fmt.Sprintf("10.0.0.%d", i)}); r.Status != 202 {
			t.Fatalf("%d-urinish: %d %v", i, r.Status, r.Body)
		}
		e.mr.FastForward(61 * time.Second) // cooldown'dan o'tish, soatlik oynadan emas
	}
	r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, map[string]string{"X-Real-IP": "10.0.0.99"})
	if r.Status != 429 || r.errKey() != "AUTH_OTP_RATE_LIMITED" {
		t.Fatalf("6-urinish bloklanishi kerak: %d %v", r.Status, r.Body)
	}
	if ra, _ := r.errParam("retry_after").(float64); ra < 1 || ra > 3600 {
		t.Fatalf("retry_after = %v", r.errParam("retry_after"))
	}
	// boshqa telefon ta'sirlanmaydi
	if r = e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": "+998907777777"}, map[string]string{"X-Real-IP": "10.0.0.98"}); r.Status != 202 {
		t.Fatalf("boshqa telefon: %d", r.Status)
	}
	// soat o'tgach yana mumkin
	e.mr.FastForward(time.Hour)
	if r = e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, map[string]string{"X-Real-IP": "10.0.0.97"}); r.Status != 202 {
		t.Fatalf("soatdan keyin: %d %v", r.Status, r.Body)
	}
}

func TestOTPIPLimitTwentyPerHour(t *testing.T) {
	e := newEnv(t)
	ip := map[string]string{"X-Real-IP": "203.0.113.7"}
	for i := 0; i < 20; i++ {
		phone := fmt.Sprintf("+99890100%04d", i)
		if r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phone}, ip); r.Status != 202 {
			t.Fatalf("%d-urinish: %d %v", i+1, r.Status, r.Body)
		}
	}
	r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": "+998909999999"}, ip)
	if r.Status != 429 || r.errKey() != "AUTH_OTP_RATE_LIMITED" {
		t.Fatalf("21-urinish bloklanishi kerak: %d %v", r.Status, r.Body)
	}
	// bloklangan IP begona telefon kvotasini yemaydi, boshqa IP esa o'tadi
	if r = e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": "+998909999999"}, map[string]string{"X-Real-IP": "203.0.113.8"}); r.Status != 202 {
		t.Fatalf("boshqa IP: %d %v", r.Status, r.Body)
	}
	// X-Forwarded-For ham qo'llab-quvvatlanadi (Kong), RemoteAddr emas
	if r = e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": "+998908888888"}, map[string]string{"X-Forwarded-For": "6.6.6.6, 203.0.113.7"}); r.Status != 429 {
		t.Fatalf("XFF bo'yicha IP limit (o'ng yozuv): %d", r.Status)
	}
	// mijoz XFF'ga o'zi soxta chap yozuv qo'shib limitdan qutula olmaydi
	if r = e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": "+998908888889"}, map[string]string{"X-Forwarded-For": "203.0.113.7, 198.51.100.1"}); r.Status != 202 {
		t.Fatalf("o'ng yozuv boshqa IP: %d", r.Status)
	}
}
