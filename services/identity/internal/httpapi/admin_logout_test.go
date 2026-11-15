package httpapi_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/identity/internal/token"
	"github.com/izgo/shared/roles"
)

// adminLogin — to'liq 3 qadamli admin kirish; admin access token qaytaradi.
func (e *env) adminLogin(email, phone string) string {
	e.t.Helper()
	r := adminStep1(e, email)
	if r.Status != 202 {
		e.t.Fatalf("step1: %d %v", r.Status, r.Body)
	}
	cid := r.str("challenge_id")
	if r = e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": cid, "email_code": e.n.emailCode(email), "phone": phone}, nil); r.Status != 202 {
		e.t.Fatalf("step2: %d %v", r.Status, r.Body)
	}
	if r = e.do("POST", "/admin/v1/auth/login/verify", map[string]string{"challenge_id": cid, "sms_code": e.n.smsCode(phone)}, nil); r.Status != 200 {
		e.t.Fatalf("step3: %d %v", r.Status, r.Body)
	}
	return r.str("access_token")
}

func TestAdminLogoutRevokesSession(t *testing.T) {
	e := newEnv(t) // AdminTTL = 1h, AccessTTL = 15m
	e.addStaff(adminEmail, adminPhone, roles.Manager, true)
	tok := e.adminLogin(adminEmail, adminPhone)
	c, err := e.tokens.Parse(tok, token.Access, token.AudAdmin)
	if err != nil {
		t.Fatal(err)
	}

	r := e.do("POST", "/admin/v1/auth/logout", nil, bearer(tok))
	if r.Status != 204 || r.Body != nil {
		t.Fatalf("logout: %d %v", r.Status, r.Body)
	}
	key := "bl:sid:" + c.SID
	if !e.mr.Exists(key) {
		t.Fatalf("%s blacklist'da yo'q", key)
	}
	// TTL = AdminTTL + 1 daqiqa (AccessTTL emas)
	if ttl := e.mr.TTL(key); ttl <= time.Hour || ttl > time.Hour+time.Minute {
		t.Fatalf("blacklist TTL %v, ~61m kutilgan", ttl)
	}

	// idempotent: bekor qilingan, lekin imzosi yaroqli token bilan qayta chaqiruv ham 204
	if r = e.do("POST", "/admin/v1/auth/logout", nil, bearer(tok)); r.Status != 204 {
		t.Fatalf("takroriy logout: %d %v", r.Status, r.Body)
	}
	// tana yuborilsa ham e'tiborsiz (422 emas)
	if r = e.do("POST", "/admin/v1/auth/logout", `{"x":1}`, bearer(tok)); r.Status != 204 {
		t.Fatalf("tanali logout: %d %v", r.Status, r.Body)
	}
}

func TestAdminLogoutRejectsBadTokens(t *testing.T) {
	e := newEnv(t)
	userAccess, userRefresh, _ := e.login("+998901234567", nil)
	driverAccess, _, _ := e.login("+998907654321", map[string]string{"X-App": "driver"})

	expired, _, err := e.tokens.WithClock(func() time.Time { return time.Now().Add(-2 * time.Hour) }).Issue(token.IssueParams{
		Type: token.Access, Audience: token.AudAdmin, Subject: uuid.NewString(), SessionID: uuid.NewString(),
		JTI: uuid.NewString(), Role: "manager", TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]map[string]string{
		"tokensiz":       nil,
		"bo'sh bearer":   {"Authorization": "Bearer "},
		"Bearer'siz":     {"Authorization": userAccess},
		"axlat":          bearer("abc.def.ghi"),
		"client token":   bearer(userAccess),
		"driver token":   bearer(driverAccess),
		"refresh token":  bearer(userRefresh),
		"muddati o'tgan": bearer(expired),
	}
	for name, hdr := range cases {
		r := e.do("POST", "/admin/v1/auth/logout", nil, hdr)
		if r.Status != 401 || r.errKey() != "AUTH_TOKEN_EXPIRED" || r.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}
	// rad etilgan foydalanuvchi tokeni sessiyasi bekor qilinmaydi
	if r := e.do("GET", "/api/v1/profile", nil, bearer(userAccess)); r.Status != 200 {
		t.Fatalf("client sessiyasi buzildi: %d", r.Status)
	}
}

func TestCodeLengthInOTPResponses(t *testing.T) {
	e := newEnv(t)
	e.addStaff(adminEmail, adminPhone, roles.Manager, true)
	e.addStaff("", "+998935556677", roles.Operator, true)

	r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": "+998901234567"}, nil)
	if r.Status != 202 || r.Body["code_length"] != float64(6) || r.Body["expires_in"] != float64(180) || r.Body["resend_after"] != float64(60) {
		t.Fatalf("otp/request: %d %v", r.Status, r.Body)
	}
	if got := e.n.smsCode("+998901234567"); len(got) != 6 {
		t.Fatalf("kod uzunligi %d", len(got))
	}

	// email qadami: haqiqiy va noma'lum email uchun bir xil maydonlar
	for _, email := range []string{adminEmail, "nobody@izgo.uz"} {
		r = adminStep1(e, email)
		if r.Status != 202 || r.Body["code_length"] != float64(6) || r.Body["expires_in"] != float64(180) || r.str("challenge_id") == "" {
			t.Fatalf("login/email %s: %d %v", email, r.Status, r.Body)
		}
	}
	cid := adminStep1(e, adminEmail).str("challenge_id")
	r = e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": cid, "email_code": e.n.emailCode(adminEmail), "phone": adminPhone}, nil)
	if r.Status != 202 || r.Body["code_length"] != float64(6) || r.str("challenge_id") != cid {
		t.Fatalf("login/sms: %d %v", r.Status, r.Body)
	}
	// email'siz operator (faqat telefon) va noma'lum telefon (soxta challenge)
	for _, phone := range []string{"+998935556677", "+998930000000"} {
		r = e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"phone": phone}, nil)
		if r.Status != 202 || r.Body["code_length"] != float64(6) || r.Body["expires_in"] != float64(180) {
			t.Fatalf("login/sms (telefon) %s: %d %v", phone, r.Status, r.Body)
		}
	}
}
