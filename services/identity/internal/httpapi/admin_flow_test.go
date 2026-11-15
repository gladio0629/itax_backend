package httpapi_test

import (
	"testing"

	"github.com/izgo/identity/internal/token"
	"github.com/izgo/shared/roles"
)

const (
	adminEmail = "boss@izgo.uz"
	adminPhone = "+998901112233"
)

func adminStep1(e *env, email string) resp {
	return e.do("POST", "/admin/v1/auth/login/email", map[string]string{"email": email}, nil)
}

func TestAdminThreeStepLogin(t *testing.T) {
	e := newEnv(t)
	e.addStaff(adminEmail, adminPhone, roles.Manager, true)

	// bo'sh joy bilan o'ralgan email — format xatosi (qat'iy validatsiya)
	if r := adminStep1(e, "  boss@izgo.uz "); r.Status != 422 {
		t.Fatalf("bo'sh joyli email: %d %v", r.Status, r.Body)
	}
	// katta-kichik harf farqi yo'q: email normallashtiriladi
	r := adminStep1(e, "Boss@IZGO.uz")
	if r.Status != 202 || r.str("challenge_id") == "" || r.Body["expires_in"] != float64(180) {
		t.Fatalf("step1: %d %v", r.Status, r.Body)
	}
	cid := r.str("challenge_id")
	emailCode := e.n.emailCode(adminEmail)
	if len(emailCode) != 6 {
		t.Fatalf("email kodi yuborilmadi: %q", emailCode)
	}

	// SMS kodi hali yo'q: 3-qadamga sakrab bo'lmaydi
	if r = e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": cid, "sms_code": "123456"}, nil); r.Status != 401 || r.errKey() != "AUTH_OTP_EXPIRED" {
		t.Fatalf("step3 sakrash: %d %v", r.Status, r.Body)
	}

	r = e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": cid, "email_code": emailCode, "phone": adminPhone}, nil)
	if r.Status != 202 || r.str("challenge_id") != cid {
		t.Fatalf("step2: %d %v", r.Status, r.Body)
	}
	smsCode := e.n.smsCode(adminPhone)
	if len(smsCode) != 6 {
		t.Fatal("SMS kodi yuborilmadi")
	}
	// email kodi bir martalik
	if r = e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": cid, "email_code": emailCode, "phone": adminPhone}, nil); r.Status != 401 {
		t.Fatalf("email kodi qayta ishlatildi: %d", r.Status)
	}

	r = e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": cid, "sms_code": smsCode}, nil)
	if r.Status != 200 || r.Body["token_type"] != "Bearer" || r.Body["refresh_token"] != nil || r.Body["expires_in"] != float64(3600) {
		t.Fatalf("step3: %d %v", r.Status, r.Body)
	}
	c, err := e.tokens.Parse(r.str("access_token"), token.Access, token.AudAdmin)
	if err != nil || c.Role != "manager" || len(c.Perms) == 0 {
		t.Fatalf("admin claims: %v %+v", err, c)
	}

	// admin token user endpointlarida ishlamaydi (alohida audience)
	if r = e.do("GET", "/api/v1/profile", nil, bearer(r.str("access_token"))); r.Status != 401 {
		t.Fatalf("admin token profilga kirdi: %d", r.Status)
	}
	// challenge bir martalik
	if r = e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": cid, "sms_code": smsCode}, nil); r.Status != 401 {
		t.Fatalf("challenge qayta ishlatildi: %d", r.Status)
	}
}

func TestAdminUnknownEmailIndistinguishable(t *testing.T) {
	e := newEnv(t)
	e.addStaff(adminEmail, adminPhone, roles.Superadmin, true)
	e.addStaff("fired@izgo.uz", "+998903334455", roles.Operator, false)

	for _, email := range []string{adminEmail, "nobody@izgo.uz", "fired@izgo.uz"} {
		r := adminStep1(e, email)
		if r.Status != 202 || r.str("challenge_id") == "" || r.Body["expires_in"] != float64(180) {
			t.Fatalf("%s: %d %v", email, r.Status, r.Body)
		}
		// noto'g'ri email kodi: hamma uchun bir xil javob
		w := e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": r.str("challenge_id"), "email_code": "000000", "phone": adminPhone}, nil)
		if email != adminEmail && w.Status != 401 {
			t.Fatalf("%s: %d", email, w.Status)
		}
		if w.errKey() != "AUTH_OTP_INVALID" || w.errParam("attempts_left") != float64(2) {
			t.Fatalf("%s: javob farq qiladi: %d %v", email, w.Status, w.Body)
		}
	}
	if e.n.emailCode("nobody@izgo.uz") != "" || e.n.emailCode("fired@izgo.uz") != "" {
		t.Fatal("noma'lum/nofaol emailga kod yuborildi")
	}
}

func TestAdminWrongPhoneAndWrongCodes(t *testing.T) {
	e := newEnv(t)
	e.addStaff(adminEmail, adminPhone, roles.Superadmin, true)

	// boshqa telefon: umumiy AUTH_OTP_INVALID, SMS yuborilmaydi
	r := adminStep1(e, adminEmail)
	cid := r.str("challenge_id")
	w := e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": cid, "email_code": e.n.emailCode(adminEmail), "phone": "+998900000000"}, nil)
	if w.Status != 401 || w.errKey() != "AUTH_OTP_INVALID" {
		t.Fatalf("noto'g'ri telefon: %d %v", w.Status, w.Body)
	}
	if e.n.smsCode("+998900000000") != "" || e.n.smsCode(adminPhone) != "" {
		t.Fatal("noto'g'ri telefonda SMS yuborildi")
	}

	// SMS bosqichida 3 noto'g'ri urinishdan keyin kod o'chadi
	e.mr.FastForward(0)
	r = adminStep1(e, adminEmail)
	cid = r.str("challenge_id")
	e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": cid, "email_code": e.n.emailCode(adminEmail), "phone": adminPhone}, nil)
	good := e.n.smsCode(adminPhone)
	bad := "000000"
	if good == bad {
		bad = "111111"
	}
	for _, left := range []float64{2, 1, 0} {
		w = e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": cid, "sms_code": bad}, nil)
		if w.Status != 401 || w.errKey() != "AUTH_OTP_INVALID" || w.errParam("attempts_left") != left {
			t.Fatalf("left=%v: %d %v", left, w.Status, w.Body)
		}
	}
	if w = e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": cid, "sms_code": good}, nil); w.Status != 401 || w.errKey() != "AUTH_OTP_EXPIRED" {
		t.Fatalf("o'chirilgan SMS kodi: %d %v", w.Status, w.Body)
	}
}

func TestAdminRateLimitAndValidation(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		if r := adminStep1(e, "x@izgo.uz"); r.Status != 202 {
			t.Fatalf("%d: %d", i, r.Status)
		}
	}
	if r := adminStep1(e, "x@izgo.uz"); r.Status != 429 {
		t.Fatalf("6-urinish: %d %v", r.Status, r.Body)
	}
	for _, body := range []any{
		map[string]string{"email": "not-an-email"},
		map[string]string{"email": "A <a@b.uz>"},
		map[string]string{},
	} {
		if r := e.do("POST", "/admin/v1/auth/login/email", body, nil); r.Status != 422 {
			t.Fatalf("%v: %d", body, r.Status)
		}
	}
	if r := e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": "zzz", "email_code": "1", "phone": "1"}, nil); r.Status != 422 {
		t.Fatalf("sms validatsiya: %d", r.Status)
	}
	if r := e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": "3fa85f64-5717-4562-b3fc-2c963f66afa6", "sms_code": "123456"}, nil); r.Status != 401 || r.errKey() != "AUTH_OTP_EXPIRED" {
		t.Fatalf("noma'lum challenge: %d %v", r.Status, r.Body)
	}
}
