package httpapi_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/izgo/identity/internal/token"
	"github.com/izgo/shared/roles"
)

const (
	operatorPhone = "+998907770001"
	internalPath  = "/internal/identity/staff"
)

func internalHdr() map[string]string { return map[string]string{"X-Internal-Token": testInternalToken} }

func phoneStep(e *env, phone string) resp {
	return e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"phone": phone}, nil)
}

func verify(e *env, cid, code string) resp {
	return e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": cid, "sms_code": code}, nil)
}

// Email'siz operator: telefon -> SMS -> token (email qadamisiz, 2 ta so'rov).
func TestOperatorPhoneOnlyLogin(t *testing.T) {
	e := newEnv(t)
	e.addStaff("", operatorPhone, roles.Operator, true)

	r := phoneStep(e, operatorPhone)
	if r.Status != 202 || r.str("challenge_id") == "" || r.Body["expires_in"] != float64(180) {
		t.Fatalf("telefon qadami: %d %v", r.Status, r.Body)
	}
	cid := r.str("challenge_id")
	code := e.n.smsCode(operatorPhone)
	if len(code) != 6 {
		t.Fatalf("SMS yuborilmadi: %q", code)
	}
	r = verify(e, cid, code)
	if r.Status != 200 || r.Body["token_type"] != "Bearer" {
		t.Fatalf("verify: %d %v", r.Status, r.Body)
	}
	c, err := e.tokens.Parse(r.str("access_token"), token.Access, token.AudAdmin)
	if err != nil || c.Role != "operator" {
		t.Fatalf("claims: %v %+v", err, c)
	}
	// challenge bir martalik
	if r = verify(e, cid, code); r.Status != 401 {
		t.Fatalf("challenge qayta ishlatildi: %d", r.Status)
	}
}

// Email'i bor xodim telefon bilan email qadamini chetlab o'ta olmaydi; noma'lum/nofaol telefon bilan
// javoblar haqiqiy operatornikidan farq qilmaydi.
func TestPhoneOnlyNoDowngradeAndIndistinguishable(t *testing.T) {
	e := newEnv(t)
	e.addStaff(adminEmail, adminPhone, roles.Manager, true) // email'i bor
	e.addStaff("", "+998907770009", roles.Operator, false)  // nofaol operator
	e.addStaff("", operatorPhone, roles.Operator, true)     // haqiqiy operator

	real := phoneStep(e, operatorPhone)
	if real.Status != 202 {
		t.Fatalf("operator: %d", real.Status)
	}
	realWrong := verify(e, real.str("challenge_id"), "000000")

	for _, phone := range []string{adminPhone, "+998907770009", "+998900000000"} {
		r := phoneStep(e, phone)
		if r.Status != 202 || r.str("challenge_id") == "" || r.Body["expires_in"] != real.Body["expires_in"] {
			t.Fatalf("%s: javob farq qiladi: %d %v", phone, r.Status, r.Body)
		}
		if c := e.n.smsCode(phone); c != "" {
			t.Fatalf("%s: SMS yuborilmasligi kerak edi", phone)
		}
		// noto'g'ri kod: haqiqiy operator bilan bir xil xato
		w := verify(e, r.str("challenge_id"), "000000")
		if w.Status != realWrong.Status || w.errKey() != realWrong.errKey() || w.errParam("attempts_left") != realWrong.errParam("attempts_left") {
			t.Fatalf("%s: verify farq qiladi: %d %v vs %d %v", phone, w.Status, w.Body, realWrong.Status, realWrong.Body)
		}
	}

	// email'i bor xodim uchun to'liq 3 qadamli oqim ishlashda davom etadi
	r := adminStep1(e, adminEmail)
	r = e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": r.str("challenge_id"), "email_code": e.n.emailCode(adminEmail), "phone": adminPhone}, nil)
	if r.Status != 202 || e.n.smsCode(adminPhone) == "" {
		t.Fatalf("3 qadamli oqim buzildi: %d %v", r.Status, r.Body)
	}
}

func TestPhoneOnlyRateLimitAndValidation(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 5; i++ {
		if r := phoneStep(e, "+998900000001"); r.Status != 202 {
			t.Fatalf("%d: %d", i, r.Status)
		}
	}
	if r := phoneStep(e, "+998900000001"); r.Status != 429 || r.errKey() != "AUTH_OTP_RATE_LIMITED" {
		t.Fatalf("6-urinish: %d %v", r.Status, r.Body)
	}
	// challenge_id va email_code birga keladi yoki ikkalasi ham yo'q
	for _, body := range []map[string]string{
		{"phone": "123"},
		{"challenge_id": uuid.NewString(), "phone": operatorPhone},
		{"email_code": "123456", "phone": operatorPhone},
		{},
	} {
		if r := e.do("POST", "/admin/v1/auth/login/sms", body, nil); r.Status != 422 {
			t.Fatalf("%v: %d %v", body, r.Status, r.Body)
		}
	}
}

func TestInternalStaffCreate(t *testing.T) {
	e := newEnv(t)
	op := map[string]any{"phone": operatorPhone, "email": nil, "role": "operator", "name": "Operator Bir"}

	// token yo'q / noto'g'ri
	for _, hdr := range []map[string]string{nil, {"X-Internal-Token": "wrong-token-0123456789"}, {"X-Internal-Token": ""}} {
		if r := e.do("POST", internalPath, op, hdr); r.Status != 403 || r.errKey() != "FORBIDDEN" || r.errParam("reason") != "internal_only" {
			t.Fatalf("%v: %d %v", hdr, r.Status, r.Body)
		}
	}

	// email null bilan operator yaratiladi
	r := e.do("POST", internalPath, op, internalHdr())
	if r.Status != 201 || r.str("phone") != operatorPhone || r.str("role") != "operator" {
		t.Fatalf("yaratish: %d %v", r.Status, r.Body)
	}
	if _, err := uuid.Parse(r.str("id")); err != nil || len(r.Body) != 3 {
		t.Fatalf("javob {id, phone, role} bo'lishi kerak: %v", r.Body)
	}

	// yaratilgan operator darhol telefon bilan kira oladi
	ch := phoneStep(e, operatorPhone)
	if v := verify(e, ch.str("challenge_id"), e.n.smsCode(operatorPhone)); v.Status != 200 {
		t.Fatalf("yangi operator kira olmadi: %d %v", v.Status, v.Body)
	}

	// email bilan manager; email normallashtiriladi
	mgr := map[string]any{"phone": "+998907770002", "email": "Manager@IZGO.uz", "role": "manager", "name": "Menejer"}
	if r = e.do("POST", internalPath, mgr, internalHdr()); r.Status != 201 {
		t.Fatalf("manager: %d %v", r.Status, r.Body)
	}
	if r = adminStep1(e, "manager@izgo.uz"); r.Status != 202 || e.n.emailCode("manager@izgo.uz") == "" {
		t.Fatalf("manager email bilan topilmadi: %d", r.Status)
	}

	// band telefon / band email
	if r = e.do("POST", internalPath, map[string]any{"phone": operatorPhone, "email": "x@izgo.uz", "role": "marketer", "name": "X"}, internalHdr()); r.Status != 409 || r.errKey() != "STAFF_PHONE_TAKEN" {
		t.Fatalf("telefon band: %d %v", r.Status, r.Body)
	}
	if r = e.do("POST", internalPath, map[string]any{"phone": "+998907770003", "email": "MANAGER@izgo.uz", "role": "marketer", "name": "X"}, internalHdr()); r.Status != 409 || r.errKey() != "STAFF_EMAIL_TAKEN" {
		t.Fatalf("email band: %d %v", r.Status, r.Body)
	}

	// validatsiya
	for _, c := range []struct {
		body  map[string]any
		field string
		want  string
	}{
		{map[string]any{"phone": "+998907770004", "email": nil, "role": "manager", "name": "X"}, "email", "required"}, // email'siz faqat operator
		{map[string]any{"phone": "+998907770004", "role": "owner", "name": "X"}, "email", "required"},                 // email maydoni yo'q
		{map[string]any{"phone": "+998907770004", "email": "", "role": "operator", "name": "X"}, "email", "invalid_format"},
		{map[string]any{"phone": "+998907770004", "email": nil, "role": "driver", "name": "X"}, "role", "invalid_value"}, // xodim roli emas
		{map[string]any{"phone": "+998907770004", "email": nil, "role": "root", "name": "X"}, "role", "invalid_value"},
		{map[string]any{"phone": "998907770004", "email": nil, "role": "operator", "name": "X"}, "phone", "invalid_format"},
		{map[string]any{"phone": "+998907770004", "email": nil, "role": "operator", "name": "  "}, "name", "required"},
		{map[string]any{"phone": "+998907770004", "email": nil, "role": "operator", "name": "X", "id": uuid.NewString()}, "id", "unknown_field"},
	} {
		r := e.do("POST", internalPath, c.body, internalHdr())
		f, _ := r.errParam("fields").(map[string]any)
		if r.Status != 422 || f[c.field] != c.want {
			t.Fatalf("%v: %d %v", c.body, r.Status, r.Body)
		}
	}
}

func staffPath(id string) string { return "/internal/identity/staff/" + id }

func TestInternalStaffUpdate(t *testing.T) {
	e := newEnv(t)
	mk := func(body map[string]any) string {
		t.Helper()
		r := e.do("POST", internalPath, body, internalHdr())
		if r.Status != 201 {
			t.Fatalf("yaratish: %d %v", r.Status, r.Body)
		}
		return r.str("id")
	}
	mgr := mk(map[string]any{"phone": "+998907770011", "email": "mgr@izgo.uz", "role": "manager", "name": "Menejer"})
	op := mk(map[string]any{"phone": "+998907770012", "email": nil, "role": "operator", "name": "Operator"})
	mk(map[string]any{"phone": "+998907770013", "email": "other@izgo.uz", "role": "marketer", "name": "Boshqa"})

	// token yo'q
	if r := e.do("PUT", staffPath(mgr), map[string]any{"name": "X"}, nil); r.Status != 403 || r.errParam("reason") != "internal_only" {
		t.Fatalf("token yo'q: %d %v", r.Status, r.Body)
	}

	// faqat yuborilgan maydon o'zgaradi
	r := e.do("PUT", staffPath(mgr), map[string]any{"name": "  Yangi Ism  "}, internalHdr())
	if r.Status != 200 || r.str("name") != "Yangi Ism" || r.str("phone") != "+998907770011" || r.str("email") != "mgr@izgo.uz" ||
		r.str("role") != "manager" || r.Body["active"] != true {
		t.Fatalf("ism: %d %v", r.Status, r.Body)
	}

	// telefon o'zgarsa — yangi telefon bilan ham to'liq oqim davom etadi (email + SMS)
	if r = e.do("PUT", staffPath(mgr), map[string]any{"phone": "+998907770021", "email": "MGR2@izgo.uz"}, internalHdr()); r.Status != 200 ||
		r.str("phone") != "+998907770021" || r.str("email") != "mgr2@izgo.uz" {
		t.Fatalf("telefon+email: %d %v", r.Status, r.Body)
	}
	ch := adminStep1(e, "mgr2@izgo.uz")
	if r = e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": ch.str("challenge_id"), "email_code": e.n.emailCode("mgr2@izgo.uz"), "phone": "+998907770021"}, nil); r.Status != 202 {
		t.Fatalf("yangi telefon bilan kirish: %d %v", r.Status, r.Body)
	}

	// rol o'zgarishi tokenga tushadi
	if r = e.do("PUT", staffPath(mgr), map[string]any{"role": "owner"}, internalHdr()); r.Status != 200 || r.str("role") != "owner" {
		t.Fatalf("rol: %d %v", r.Status, r.Body)
	}
	if v := verify(e, ch.str("challenge_id"), e.n.smsCode("+998907770021")); v.Status != 200 {
		t.Fatalf("verify: %d %v", v.Status, v.Body)
	} else if c, err := e.tokens.Parse(v.str("access_token"), token.Access, token.AudAdmin); err != nil || c.Role != "owner" {
		t.Fatalf("token roli: %v %+v", err, c)
	}

	// band telefon / band email (boshqa xodimniki); o'zining qiymati — to'qnashuv emas
	if r = e.do("PUT", staffPath(mgr), map[string]any{"phone": "+998907770013"}, internalHdr()); r.Status != 409 || r.errKey() != "STAFF_PHONE_TAKEN" {
		t.Fatalf("telefon band: %d %v", r.Status, r.Body)
	}
	if r = e.do("PUT", staffPath(mgr), map[string]any{"email": "Other@izgo.uz"}, internalHdr()); r.Status != 409 || r.errKey() != "STAFF_EMAIL_TAKEN" {
		t.Fatalf("email band: %d %v", r.Status, r.Body)
	}
	if r = e.do("PUT", staffPath(mgr), map[string]any{"phone": "+998907770021"}, internalHdr()); r.Status != 200 {
		t.Fatalf("o'z telefoni: %d %v", r.Status, r.Body)
	}

	// email'siz faqat operator: email olib tashlash / email'siz operatorni boshqa rolga o'tkazish
	if r = e.do("PUT", staffPath(mgr), map[string]any{"email": nil}, internalHdr()); r.Status != 422 {
		t.Fatalf("owner email null: %d %v", r.Status, r.Body)
	}
	if r = e.do("PUT", staffPath(op), map[string]any{"role": "manager"}, internalHdr()); r.Status != 422 {
		t.Fatalf("email'siz operator -> manager: %d %v", r.Status, r.Body)
	}
	if r = e.do("PUT", staffPath(op), map[string]any{"role": "manager", "email": "op@izgo.uz"}, internalHdr()); r.Status != 200 || r.str("email") != "op@izgo.uz" {
		t.Fatalf("operator -> manager + email: %d %v", r.Status, r.Body)
	}
	if r = e.do("PUT", staffPath(op), map[string]any{"role": "operator", "email": nil}, internalHdr()); r.Status != 200 || r.Body["email"] != nil {
		t.Fatalf("manager -> operator, email null: %d %v", r.Status, r.Body)
	}

	// bloklash: nofaol xodim kira olmaydi (telefon qadamida ham SMS yuborilmaydi)
	if r = e.do("PUT", staffPath(op), map[string]any{"active": false}, internalHdr()); r.Status != 200 || r.Body["active"] != false {
		t.Fatalf("bloklash: %d %v", r.Status, r.Body)
	}
	phoneStep(e, "+998907770012")
	if c := e.n.smsCode("+998907770012"); c != "" {
		t.Fatal("bloklangan operatorga SMS yuborildi")
	}

	// topilmadi / validatsiya
	if r = e.do("PUT", staffPath(uuid.NewString()), map[string]any{"name": "X"}, internalHdr()); r.Status != 404 || r.errKey() != "STAFF_NOT_FOUND" {
		t.Fatalf("topilmadi: %d %v", r.Status, r.Body)
	}
	for _, c := range []struct {
		body  map[string]any
		field string
		want  string
	}{
		{map[string]any{}, "body", "empty"},
		{map[string]any{"phone": nil}, "phone", "invalid_value"},
		{map[string]any{"phone": "123"}, "phone", "invalid_format"},
		{map[string]any{"role": "driver"}, "role", "invalid_value"},
		{map[string]any{"role": nil}, "role", "invalid_value"},
		{map[string]any{"name": " "}, "name", "required"},
		{map[string]any{"email": "not-email"}, "email", "invalid_format"},
		{map[string]any{"id": uuid.NewString()}, "id", "unknown_field"},
	} {
		r := e.do("PUT", staffPath(mgr), c.body, internalHdr())
		f, _ := r.errParam("fields").(map[string]any)
		if r.Status != 422 || f[c.field] != c.want {
			t.Fatalf("%v: %d %v", c.body, r.Status, r.Body)
		}
	}
	if r = e.do("PUT", staffPath("not-a-uuid"), map[string]any{"name": "X"}, internalHdr()); r.Status != 422 {
		t.Fatalf("id UUID emas: %d", r.Status)
	}
}

func TestInternalUserLookup(t *testing.T) {
	e := newEnv(t)
	_, _, pid := e.login("+998901234501", nil)
	_, _, did := e.login("+998901234502", map[string]string{"X-App": "driver"})
	tok, _, _ := e.login("+998901234503", nil)
	if r := e.do("PUT", "/api/v1/profile", map[string]any{"first_name": "Ali", "last_name": "Valiyev"}, bearer(tok)); r.Status != 200 {
		t.Fatalf("profil: %d", r.Status)
	}
	var named uuid.UUID
	if c, err := e.tokens.Parse(tok, token.Access, token.AudClient); err == nil {
		named = uuid.MustParse(c.Subject)
	}

	path := func(id string) string { return "/internal/identity/users/" + id }
	if r := e.do("GET", path(pid.String()), nil, nil); r.Status != 403 || r.errParam("reason") != "internal_only" {
		t.Fatalf("token yo'q: %d %v", r.Status, r.Body)
	}

	r := e.do("GET", path(pid.String()), nil, internalHdr())
	if r.Status != 200 || r.str("id") != pid.String() || r.str("phone") != "+998901234501" || r.str("role") != "passenger" ||
		r.Body["firstName"] != nil || r.Body["lastName"] != nil || len(r.Body) != 5 {
		t.Fatalf("yo'lovchi: %d %v", r.Status, r.Body)
	}
	if r = e.do("GET", path(did.String()), nil, internalHdr()); r.Status != 200 || r.str("role") != "driver" {
		t.Fatalf("haydovchi: %d %v", r.Status, r.Body)
	}
	if r = e.do("GET", path(named.String()), nil, internalHdr()); r.str("firstName") != "Ali" || r.str("lastName") != "Valiyev" {
		t.Fatalf("ism: %v", r.Body)
	}
	if r = e.do("GET", path(uuid.NewString()), nil, internalHdr()); r.Status != 404 || r.errKey() != "NOT_FOUND" {
		t.Fatalf("topilmadi: %d %v", r.Status, r.Body)
	}
	if r = e.do("GET", path("abc"), nil, internalHdr()); r.Status != 422 {
		t.Fatalf("id UUID emas: %d", r.Status)
	}
}
