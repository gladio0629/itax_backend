package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/izgo/identity/internal/apperr"
	"github.com/izgo/identity/internal/auth"
	"github.com/izgo/shared/roles"
)

const maxBody = 16 << 10

var (
	phoneRe     = regexp.MustCompile(`^\+998[0-9]{9}$`)
	otpCodeRe   = regexp.MustCompile(`^[0-9]{4,6}$`)
	emailCodeRe = regexp.MustCompile(`^[0-9]{6}$`)
)

// validator — har bir so'rov DTO'si o'zini tekshiradi: maydon -> xato kaliti.
type validator interface{ validate() map[string]string }

// decode — qat'iy JSON (noma'lum maydon taqiqlangan, bitta qiymat) + validatsiya.
// Shartnomada 400 yo'q, shuning uchun buzuq JSON ham 422 VALIDATION_ERROR.
func decode(w http.ResponseWriter, r *http.Request, dst validator) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return apperr.Validation(jsonErrFields(err))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return apperr.Validation(map[string]string{"body": "invalid_json"})
	}
	if f := dst.validate(); len(f) > 0 {
		return apperr.Validation(f)
	}
	return nil
}

func jsonErrFields(err error) map[string]string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return map[string]string{te.Field: "invalid_type"}
	}
	if s := err.Error(); strings.HasPrefix(s, "json: unknown field ") {
		return map[string]string{strings.Trim(strings.TrimPrefix(s, "json: unknown field "), `"`): "unknown_field"}
	}
	return map[string]string{"body": "invalid_json"}
}

func checkPhone(f map[string]string, name, v string) {
	if v == "" {
		f[name] = "required"
	} else if !phoneRe.MatchString(v) {
		f[name] = "invalid_format"
	}
}

func checkRegexp(f map[string]string, name, v string, re *regexp.Regexp) {
	if v == "" {
		f[name] = "required"
	} else if !re.MatchString(v) {
		f[name] = "invalid_format"
	}
}

func checkUUID(f map[string]string, name, v string) {
	if v == "" {
		f[name] = "required"
	} else if _, err := uuid.Parse(v); err != nil {
		f[name] = "invalid_format"
	}
}

// ---- auth

type otpRequestDTO struct {
	Phone string `json:"phone"`
}

func (d *otpRequestDTO) validate() map[string]string {
	f := map[string]string{}
	checkPhone(f, "phone", d.Phone)
	return f
}

type otpRequestResp struct {
	ExpiresIn   int `json:"expires_in"`
	ResendAfter int `json:"resend_after"`
}

type otpVerifyDTO struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

func (d *otpVerifyDTO) validate() map[string]string {
	f := map[string]string{}
	checkPhone(f, "phone", d.Phone)
	checkRegexp(f, "code", d.Code, otpCodeRe)
	return f
}

type refreshDTO struct {
	RefreshToken string `json:"refresh_token"`
}

func (d *refreshDTO) validate() map[string]string {
	f := map[string]string{}
	if d.RefreshToken == "" {
		f["refresh_token"] = "required"
	} else if len(d.RefreshToken) > 4096 {
		f["refresh_token"] = "too_long"
	}
	return f
}

type tokenPairResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	IsNewUser    *bool  `json:"is_new_user,omitempty"`
}

func newTokenPairResp(p auth.TokenPair) tokenPairResp {
	return tokenPairResp{AccessToken: p.AccessToken, RefreshToken: p.RefreshToken, ExpiresIn: p.ExpiresIn, TokenType: "Bearer", IsNewUser: p.IsNewUser}
}

// ---- admin auth

type adminEmailDTO struct {
	Email string `json:"email"`
}

func (d *adminEmailDTO) validate() map[string]string {
	f := map[string]string{}
	switch addr, err := mail.ParseAddress(d.Email); {
	case d.Email == "":
		f["email"] = "required"
	case len(d.Email) > 254 || err != nil || addr.Address != d.Email:
		f["email"] = "invalid_format"
	}
	return f
}

// adminSMSDTO — ikki ko'rinish: {challenge_id, email_code, phone} (email'li xodim, 2-qadam) yoki
// faqat {phone} (email'siz operator). challenge_id va email_code birga keladi yoki ikkalasi ham yo'q.
type adminSMSDTO struct {
	ChallengeID string `json:"challenge_id"`
	EmailCode   string `json:"email_code"`
	Phone       string `json:"phone"`
}

func (d *adminSMSDTO) phoneOnly() bool { return d.ChallengeID == "" && d.EmailCode == "" }

func (d *adminSMSDTO) validate() map[string]string {
	f := map[string]string{}
	if !d.phoneOnly() {
		checkUUID(f, "challenge_id", d.ChallengeID)
		checkRegexp(f, "email_code", d.EmailCode, emailCodeRe)
	}
	checkPhone(f, "phone", d.Phone)
	return f
}

type adminVerifyDTO struct {
	ChallengeID string `json:"challenge_id"`
	SMSCode     string `json:"sms_code"`
}

func (d *adminVerifyDTO) validate() map[string]string {
	f := map[string]string{}
	checkUUID(f, "challenge_id", d.ChallengeID)
	checkRegexp(f, "sms_code", d.SMSCode, otpCodeRe)
	return f
}

type challengeResp struct {
	ChallengeID uuid.UUID `json:"challenge_id"`
	ExpiresIn   int       `json:"expires_in"`
}

type adminTokenResp struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// ---- internal (Backoffice -> Identity)

// internalStaffDTO — email null (yoki yo'q) bo'lishi mumkin: faqat operator uchun (servis tekshiradi).
type internalStaffDTO struct {
	Phone string  `json:"phone"`
	Email *string `json:"email"`
	Role  string  `json:"role"`
	Name  string  `json:"name"`
}

func (d *internalStaffDTO) validate() map[string]string {
	f := map[string]string{}
	checkPhone(f, "phone", d.Phone)
	if d.Email != nil {
		switch addr, err := mail.ParseAddress(*d.Email); {
		case *d.Email == "":
			f["email"] = "invalid_format" // bo'sh satr emas, null yuborilsin
		case len(*d.Email) > 254 || err != nil || addr.Address != *d.Email:
			f["email"] = "invalid_format"
		}
	}
	if d.Role == "" {
		f["role"] = "required"
	} else if !roles.Role(d.Role).IsStaff() {
		f["role"] = "invalid_value"
	}
	switch n := utf8.RuneCountInString(strings.TrimSpace(d.Name)); {
	case n == 0:
		f["name"] = "required"
	case n > 100:
		f["name"] = "too_long"
	}
	return f
}

type internalStaffResp struct {
	ID    uuid.UUID `json:"id"`
	Phone string    `json:"phone"`
	Role  string    `json:"role"`
}

// optString — JSON maydoni uchun uch holat: yo'q (Set=false), null (Set=true, Val=nil), qiymat.
type optString struct {
	Set bool
	Val *string
}

func (o *optString) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Val = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	o.Val = &s
	return nil
}

// internalStaffUpdateDTO — PUT /internal/identity/staff/{id}: faqat yuborilgan maydonlar o'zgaradi.
// email: null — email olib tashlanadi (faqat operator uchun; servis yakuniy holatni tekshiradi).
// phone/role/name/active uchun null ruxsat etilmaydi.
type internalStaffUpdateDTO struct {
	Phone  optString `json:"phone"`
	Email  optString `json:"email"`
	Role   optString `json:"role"`
	Name   optString `json:"name"`
	Active *bool     `json:"active"`
}

func (d *internalStaffUpdateDTO) validate() map[string]string {
	f := map[string]string{}
	notNull := func(name string, o optString) bool {
		if o.Set && o.Val == nil {
			f[name] = "invalid_value"
			return false
		}
		return o.Set
	}
	if notNull("phone", d.Phone) {
		checkPhone(f, "phone", *d.Phone.Val)
	}
	if d.Email.Set && d.Email.Val != nil {
		switch addr, err := mail.ParseAddress(*d.Email.Val); {
		case len(*d.Email.Val) > 254 || err != nil || addr.Address != *d.Email.Val:
			f["email"] = "invalid_format"
		}
	}
	if notNull("role", d.Role) && !roles.Role(*d.Role.Val).IsStaff() {
		f["role"] = "invalid_value"
	}
	if notNull("name", d.Name) {
		switch n := utf8.RuneCountInString(strings.TrimSpace(*d.Name.Val)); {
		case n == 0:
			f["name"] = "required"
		case n > 100:
			f["name"] = "too_long"
		}
	}
	if !d.Phone.Set && !d.Email.Set && !d.Role.Set && !d.Name.Set && d.Active == nil {
		f["body"] = "empty"
	}
	return f
}

func (d *internalStaffUpdateDTO) patch() auth.StaffPatch {
	p := auth.StaffPatch{Phone: d.Phone.Val, SetEmail: d.Email.Set, Email: d.Email.Val, Active: d.Active}
	if d.Role.Val != nil {
		r := roles.Role(*d.Role.Val)
		p.Role = &r
	}
	if d.Name.Val != nil {
		n := strings.TrimSpace(*d.Name.Val)
		p.Name = &n
	}
	return p
}

type internalStaffFullResp struct {
	ID     uuid.UUID `json:"id"`
	Phone  string    `json:"phone"`
	Email  *string   `json:"email"`
	Role   string    `json:"role"`
	Name   string    `json:"name"`
	Active bool      `json:"active"`
}

func newInternalStaffFullResp(s auth.Staff) internalStaffFullResp {
	return internalStaffFullResp{ID: s.ID, Phone: s.Phone, Email: s.Email, Role: string(s.Role), Name: s.FullName, Active: s.Active}
}

// internalUserResp — GET /internal/identity/users/{id} (Backoffice lookup). Maydon nomlari Backoffice
// shartnomasi bo'yicha camelCase.
type internalUserResp struct {
	ID        uuid.UUID `json:"id"`
	Phone     string    `json:"phone"`
	FirstName *string   `json:"firstName"`
	LastName  *string   `json:"lastName"`
	Role      string    `json:"role"`
}

// ---- profile

type profileUpdateDTO struct {
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
}

func (d *profileUpdateDTO) validate() map[string]string {
	f := map[string]string{}
	if d.FirstName == nil && d.LastName == nil {
		f["body"] = "at_least_one_field"
		return f
	}
	for name, p := range map[string]**string{"first_name": &d.FirstName, "last_name": &d.LastName} {
		if *p == nil {
			continue
		}
		v := strings.TrimSpace(**p)
		if n := utf8.RuneCountInString(v); n < 1 || n > 50 {
			f[name] = "invalid_length"
		}
		*p = &v
	}
	return f
}

type settingsDTO struct {
	Language      *string `json:"language"`
	Theme         *string `json:"theme"`
	Notifications *struct {
		Push  *bool `json:"push"`
		SMS   *bool `json:"sms"`
		Promo *bool `json:"promo"`
	} `json:"notifications"`
}

func (d *settingsDTO) validate() map[string]string {
	f := map[string]string{}
	if d.Language != nil && !oneOf(*d.Language, "uz", "ru", "en") {
		f["language"] = "invalid_value"
	}
	if d.Theme != nil && !oneOf(*d.Theme, "light", "dark", "system") {
		f["theme"] = "invalid_value"
	}
	return f
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func (d *settingsDTO) update() auth.SettingsUpdate {
	u := auth.SettingsUpdate{Language: d.Language, Theme: d.Theme}
	if n := d.Notifications; n != nil {
		u.Notifications = &auth.NotificationsUpdate{Push: n.Push, SMS: n.SMS, Promo: n.Promo}
	}
	return u
}

type settingsResp struct {
	Language      string            `json:"language"`
	Theme         string            `json:"theme"`
	Notifications notificationsResp `json:"notifications"`
}

type notificationsResp struct {
	Push  bool `json:"push"`
	SMS   bool `json:"sms"`
	Promo bool `json:"promo"`
}

func newSettingsResp(s auth.Settings) settingsResp {
	return settingsResp{Language: s.Language, Theme: s.Theme,
		Notifications: notificationsResp{Push: s.Notifications.Push, SMS: s.Notifications.SMS, Promo: s.Notifications.Promo}}
}

type profileResp struct {
	ID        uuid.UUID    `json:"id"`
	Phone     string       `json:"phone"`
	FirstName *string      `json:"first_name"`
	LastName  *string      `json:"last_name"`
	Role      string       `json:"role"`
	Rating    *float64     `json:"rating"`
	Settings  settingsResp `json:"settings"`
	CreatedAt time.Time    `json:"created_at"`
}

func newProfileResp(u auth.User) profileResp {
	return profileResp{ID: u.ID, Phone: u.Phone, FirstName: u.FirstName, LastName: u.LastName, Role: string(u.Role),
		Rating: u.Rating, Settings: newSettingsResp(u.Settings), CreatedAt: u.CreatedAt.UTC()}
}
