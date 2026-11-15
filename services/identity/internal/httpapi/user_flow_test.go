package httpapi_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/izgo/identity/internal/token"
)

const phoneA = "+998901234567"

func TestFullUserFlow(t *testing.T) {
	e := newEnv(t)

	// 1. OTP so'rash
	r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": phoneA}, nil)
	if r.Status != 202 || r.Body["expires_in"] != float64(180) || r.Body["resend_after"] != float64(60) {
		t.Fatalf("otp/request: %d %v", r.Status, r.Body)
	}
	code := e.n.smsCode(phoneA)
	if len(code) != 6 {
		t.Fatalf("kod 6 xonali bo'lishi kerak: %q", code)
	}

	// 2. tasdiqlash
	r = e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phoneA, "code": code}, nil)
	if r.Status != 200 || r.Body["token_type"] != "Bearer" || r.Body["is_new_user"] != true || r.Body["expires_in"] != float64(900) {
		t.Fatalf("otp/verify: %d %v", r.Status, r.Body)
	}
	access1, refresh1 := r.str("access_token"), r.str("refresh_token")

	// access token ichida role/permissions/audience bor
	c, err := e.tokens.Parse(access1, token.Access, token.AudClient)
	if err != nil {
		t.Fatal(err)
	}
	if c.Role != "passenger" || len(c.Perms) == 0 || c.Aud() != token.AudClient {
		t.Fatalf("claims: %+v", c)
	}
	if got := c.ExpiresAt.Sub(c.IssuedAt.Time); got != 15*time.Minute {
		t.Fatalf("access TTL = %v", got)
	}
	rc, err := e.tokens.Parse(refresh1, token.Refresh, token.AudClient)
	if err != nil {
		t.Fatal(err)
	}
	if got := rc.ExpiresAt.Sub(rc.IssuedAt.Time); got != 7*24*time.Hour {
		t.Fatalf("refresh TTL = %v", got)
	}

	// kod bir martalik
	if r = e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phoneA, "code": code}, nil); r.Status != 401 || r.errKey() != "AUTH_OTP_EXPIRED" {
		t.Fatalf("kod qayta ishlatildi: %d %v", r.Status, r.Body)
	}

	// user.created hodisasi outbox'da, kontrakt bo'yicha
	if len(e.repo.Outbox) != 1 {
		t.Fatalf("outbox: %d ta hodisa", len(e.repo.Outbox))
	}
	ev := e.repo.Outbox[0]
	var env struct {
		Event, TraceID string
		Version        int
		EventID        string `json:"event_id"`
		OccurredAt     string `json:"occurred_at"`
		Payload        map[string]string
	}
	if err := json.Unmarshal(ev.Payload, &env); err != nil {
		t.Fatal(err)
	}
	if ev.Subject != "izgo.user.created.v1" || env.Event != "user.created" || env.Version != 1 ||
		env.EventID != ev.ID.String() || env.Payload["phone"] != phoneA || env.Payload["role"] != "passenger" ||
		env.Payload["user_id"] != c.Subject || env.Payload["created_at"] == "" || env.OccurredAt == "" {
		t.Fatalf("hodisa: %s", ev.Payload)
	}

	// 3. profil
	r = e.do("GET", "/api/v1/profile", nil, bearer(access1))
	if r.Status != 200 || r.Body["phone"] != phoneA || r.Body["role"] != "passenger" || r.Body["id"] != c.Subject ||
		r.Body["first_name"] != nil || r.Body["rating"] != nil {
		t.Fatalf("profile: %d %v", r.Status, r.Body)
	}

	// 4. refresh: yangi juftlik
	r = e.do("POST", "/api/v1/auth/token/refresh", map[string]string{"refresh_token": refresh1}, nil)
	if r.Status != 200 || r.Body["is_new_user"] != nil {
		t.Fatalf("refresh: %d %v", r.Status, r.Body)
	}
	access2, refresh2 := r.str("access_token"), r.str("refresh_token")
	if refresh2 == refresh1 {
		t.Fatal("refresh rotation ishlamadi")
	}
	if r = e.do("GET", "/api/v1/profile", nil, bearer(access2)); r.Status != 200 {
		t.Fatalf("yangi access: %d", r.Status)
	}

	// 5. ishlatilgan refresh qayta kelsa — HAMMA tokenlar bekor
	r = e.do("POST", "/api/v1/auth/token/refresh", map[string]string{"refresh_token": refresh1}, nil)
	if r.Status != 401 || r.errKey() != "AUTH_TOKEN_EXPIRED" {
		t.Fatalf("reuse: %d %v", r.Status, r.Body)
	}
	if r = e.do("POST", "/api/v1/auth/token/refresh", map[string]string{"refresh_token": refresh2}, nil); r.Status != 401 {
		t.Fatalf("reuse'dan keyin yangi refresh ishlamasligi kerak: %d", r.Status)
	}
	for name, a := range map[string]string{"access1": access1, "access2": access2} {
		if r = e.do("GET", "/api/v1/profile", nil, bearer(a)); r.Status != 401 {
			t.Fatalf("%s reuse'dan keyin ishlamasligi kerak: %d", name, r.Status)
		}
	}

	// 6. qayta kirish: mavjud foydalanuvchi, yangi hodisa yo'q
	e.mr.FastForward(2 * time.Minute) // cooldown tugasin
	access3, refresh3, id := e.login(phoneA, nil)
	if id.String() != c.Subject || len(e.repo.Outbox) != 1 {
		t.Fatalf("mavjud foydalanuvchi qayta yaratildi (outbox=%d)", len(e.repo.Outbox))
	}

	// 7. logout: noto'g'ri refresh bilan emas, to'g'risi bilan
	if r = e.do("POST", "/api/v1/auth/logout", map[string]string{"refresh_token": refresh3}, nil); r.Status != 401 {
		t.Fatalf("access'siz logout: %d", r.Status)
	}
	if r = e.do("POST", "/api/v1/auth/logout", map[string]string{"refresh_token": refresh3}, bearer(access3)); r.Status != 204 {
		t.Fatalf("logout: %d %v", r.Status, r.Body)
	}
	if r = e.do("GET", "/api/v1/profile", nil, bearer(access3)); r.Status != 401 {
		t.Fatalf("logout'dan keyin access ishlamasligi kerak: %d", r.Status)
	}
	if r = e.do("POST", "/api/v1/auth/token/refresh", map[string]string{"refresh_token": refresh3}, nil); r.Status != 401 {
		t.Fatalf("logout'dan keyin refresh ishlamasligi kerak: %d", r.Status)
	}
	// logout'dan keyin (revoked, ishlatilmagan) refresh — "reuse" emas, boshqa sessiyalarga tegmaydi
}

func TestLogoutRejectsForeignRefresh(t *testing.T) {
	e := newEnv(t)
	accessA, _, _ := e.login("+998901111111", nil)
	_, refreshB, _ := e.login("+998902222222", nil)
	if r := e.do("POST", "/api/v1/auth/logout", map[string]string{"refresh_token": refreshB}, bearer(accessA)); r.Status != 401 {
		t.Fatalf("boshqa foydalanuvchi refresh'i bilan logout: %d", r.Status)
	}
	// B ning sessiyasi tirik qoldi
	if r := e.do("POST", "/api/v1/auth/token/refresh", map[string]string{"refresh_token": refreshB}, nil); r.Status != 200 {
		t.Fatalf("B sessiyasi buzildi: %d", r.Status)
	}
}

func TestReuseRevokesAllSessionsOfUser(t *testing.T) {
	e := newEnv(t)
	// bir foydalanuvchining ikkita qurilmasi
	_, refreshPhone, id := e.login(phoneA, nil)
	e.mr.FastForward(2 * time.Minute)
	accessTablet, refreshTablet, _ := e.login(phoneA, nil)

	r := e.do("POST", "/api/v1/auth/token/refresh", map[string]string{"refresh_token": refreshPhone}, nil)
	if r.Status != 200 {
		t.Fatal(r.Status)
	}
	// o'g'ri yoki o'g'ri — eski token qayta kelsa
	if r = e.do("POST", "/api/v1/auth/token/refresh", map[string]string{"refresh_token": refreshPhone}, nil); r.Status != 401 {
		t.Fatal(r.Status)
	}
	if n := e.repo.LiveRefreshCount(id); n != 0 {
		t.Fatalf("%d ta refresh tirik qoldi", n)
	}
	if r = e.do("GET", "/api/v1/profile", nil, bearer(accessTablet)); r.Status != 401 {
		t.Fatalf("planshet access'i tirik qoldi: %d", r.Status)
	}
	if r = e.do("POST", "/api/v1/auth/token/refresh", map[string]string{"refresh_token": refreshTablet}, nil); r.Status != 401 {
		t.Fatalf("planshet refresh'i tirik qoldi: %d", r.Status)
	}
}

func TestDriverAppGetsDriverAudience(t *testing.T) {
	e := newEnv(t)
	access, _, _ := e.login(phoneA, map[string]string{"X-App": "driver"})
	c, err := e.tokens.Parse(access, token.Access, token.AudDriver)
	if err != nil || c.Role != "driver" {
		t.Fatalf("driver token: %v %+v", err, c)
	}
	if _, err := e.tokens.Parse(access, token.Access, token.AudClient); err == nil {
		t.Fatal("driver tokeni client audience bilan qabul qilindi")
	}
	if r := e.do("GET", "/api/v1/profile", nil, bearer(access)); r.Status != 200 || r.Body["role"] != "driver" {
		t.Fatalf("profile: %d %v", r.Status, r.Body)
	}
	// bir telefon — ikki alohida akkaunt (yo'lovchi + haydovchi)
	e.mr.FastForward(2 * time.Minute)
	_, _, passengerID := e.login(phoneA, nil)
	if c.Subject == passengerID.String() {
		t.Fatal("yo'lovchi va haydovchi bitta akkaunt bo'lib qoldi")
	}
	var roleSeen []string
	for _, ev := range e.repo.Outbox {
		var p struct{ Payload map[string]string }
		_ = json.Unmarshal(ev.Payload, &p)
		roleSeen = append(roleSeen, p.Payload["role"])
	}
	if fmt.Sprint(roleSeen) != "[driver passenger]" {
		t.Fatalf("hodisalar: %v", roleSeen)
	}

	if r := e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phoneA, "code": "123456"}, map[string]string{"X-App": "admin"}); r.Status != 422 {
		t.Fatalf("noma'lum X-App: %d", r.Status)
	}
}

func TestTokenMisuseRejected(t *testing.T) {
	e := newEnv(t)
	access, refresh, _ := e.login(phoneA, nil)

	cases := map[string]string{
		"refresh token access sifatida": refresh,
		"bo'sh":                         "",
		"axlat":                         "abc.def.ghi",
	}
	for name, tok := range cases {
		if r := e.do("GET", "/api/v1/profile", nil, bearer(tok)); r.Status != 401 || r.errKey() != "AUTH_TOKEN_EXPIRED" {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}
	if r := e.do("GET", "/api/v1/profile", nil, nil); r.Status != 401 {
		t.Errorf("sarlavhasiz: %d", r.Status)
	}
	// access token refresh sifatida
	if r := e.do("POST", "/api/v1/auth/token/refresh", map[string]string{"refresh_token": access}, nil); r.Status != 401 {
		t.Errorf("access refresh sifatida: %d", r.Status)
	}
}

func TestProfileAndSettings(t *testing.T) {
	e := newEnv(t)
	access, _, _ := e.login(phoneA, nil)
	h := bearer(access)

	r := e.do("PUT", "/api/v1/profile", map[string]string{"first_name": "  Ali ", "last_name": "Valiyev"}, h)
	if r.Status != 200 || r.Body["first_name"] != "Ali" || r.Body["last_name"] != "Valiyev" {
		t.Fatalf("profile put: %d %v", r.Status, r.Body)
	}
	// qisman yangilash ikkinchi maydonga tegmaydi
	r = e.do("PUT", "/api/v1/profile", map[string]string{"first_name": "Vali"}, h)
	if r.Body["first_name"] != "Vali" || r.Body["last_name"] != "Valiyev" {
		t.Fatalf("partial: %v", r.Body)
	}

	for name, body := range map[string]any{
		"bo'sh":            map[string]string{},
		"bo'sh ism":        map[string]string{"first_name": "   "},
		"uzun ism":         map[string]string{"first_name": string(make([]byte, 51))},
		"readOnly phone":   map[string]string{"phone": "+998900000000"},
		"readOnly role":    map[string]string{"role": "driver"},
		"noto'g'ri tur":    `{"first_name": 5}`,
		"buzuq json":       `{"first_name":`,
		"ikki json qiymat": `{"first_name":"A"} {"x":1}`,
	} {
		if r := e.do("PUT", "/api/v1/profile", body, h); r.Status != 422 || r.errKey() != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}

	// sozlamalar: default
	r = e.do("GET", "/api/v1/profile", nil, h)
	s := r.Body["settings"].(map[string]any)
	if s["language"] != "uz" || s["theme"] != "system" {
		t.Fatalf("default settings: %v", s)
	}
	// qisman sozlama — merge
	r = e.do("PUT", "/api/v1/profile/settings", map[string]any{"language": "ru", "notifications": map[string]bool{"promo": false}}, h)
	n := r.Body["notifications"].(map[string]any)
	if r.Status != 200 || r.Body["language"] != "ru" || r.Body["theme"] != "system" || n["promo"] != false || n["push"] != true {
		t.Fatalf("settings put: %d %v", r.Status, r.Body)
	}
	if r = e.do("GET", "/api/v1/profile", nil, h); r.Body["settings"].(map[string]any)["language"] != "ru" {
		t.Fatalf("settings saqlanmadi: %v", r.Body)
	}
	for name, body := range map[string]any{
		"til":        map[string]string{"language": "de"},
		"tema":       map[string]string{"theme": "blue"},
		"noma'lum":   map[string]string{"font": "big"},
		"ichki noma": map[string]any{"notifications": map[string]bool{"email": true}},
	} {
		if r := e.do("PUT", "/api/v1/profile/settings", body, h); r.Status != 422 {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}
}

func TestValidationErrorsUseContractFormat(t *testing.T) {
	e := newEnv(t)
	r := e.do("POST", "/api/v1/auth/otp/request", map[string]string{"phone": "998901234567"}, nil)
	if r.Status != 422 || r.errKey() != "VALIDATION_ERROR" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	fields, _ := r.errParam("fields").(map[string]any)
	if fields["phone"] != "invalid_format" {
		t.Fatalf("fields: %v", fields)
	}
	if r = e.do("POST", "/api/v1/auth/otp/verify", map[string]string{"phone": phoneA, "code": "12"}, nil); r.Status != 422 {
		t.Fatalf("qisqa kod: %d", r.Status)
	}
	if r = e.do("POST", "/api/v1/auth/otp/request", `{"phone":"+998901234567","x":1}`, nil); r.Status != 422 {
		t.Fatalf("noma'lum maydon: %d", r.Status)
	}
	if r = e.do("GET", "/nope", nil, nil); r.Status != 404 || r.errKey() != "NOT_FOUND" {
		t.Fatalf("404: %d %v", r.Status, r.Body)
	}
	if r = e.do("POST", "/api/v1/auth/token/refresh", map[string]string{}, nil); r.Status != 422 {
		t.Fatalf("refresh bo'sh: %d", r.Status)
	}
}
