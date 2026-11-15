package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/identity/internal/apperr"
	"github.com/izgo/identity/internal/token"
	"github.com/izgo/shared/roles"
)

// Admin 2 qadamli kirish (3 ta so'rov):
//  1. email           -> email'ga kod            (challenge_id)
//  2. email kodi+tel  -> SMS kod                 (xuddi shu challenge_id)
//  3. SMS kodi        -> admin token
//
// Email'siz operator (staff_users.email IS NULL) 1-qadamni o'tkazib yuboradi (2 ta so'rov):
//  2'. faqat telefon  -> SMS kod                 (yangi challenge_id)
//  3.  SMS kodi       -> admin token
// Email'i bor xodim 2'-yo'l bilan kira olmaydi (email omili chetlab o'tilmasin).
//
// Xodim mavjudligi oshkor bo'lmasligi uchun noma'lum/nofaol email yoki telefon uchun ham xuddi
// shunday challenge yaratiladi (tasodifiy, hech kimga yuborilmaydigan kod bilan): javoblar bir xil.

const decoy = "-"

func admMetaKey(cid string) string  { return "adm:meta:" + cid }
func admEmailKey(cid string) string { return "adm:email:" + cid }
func admSMSKey(cid string) string   { return "adm:sms:" + cid }

type Challenge struct {
	ID         uuid.UUID
	ExpiresIn  int
	CodeLength int // keyingi qadamda kutiladigan kod xonalari (soxta challenge'da ham bir xil)
}

func NormalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func (s *Service) AdminLoginEmail(ctx context.Context, email, ip string) (Challenge, error) {
	email = NormalizeEmail(email)
	if err := s.consume(ctx, "rl:adm:ip:"+ip, s.opt.IPLimit); err != nil {
		return Challenge{}, err
	}
	if err := s.consume(ctx, "rl:adm:email:"+email, s.opt.PhoneLimit); err != nil {
		return Challenge{}, err
	}

	staff, err := s.repo.GetStaffByEmail(ctx, email)
	known := err == nil && staff.Active
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Challenge{}, fmt.Errorf("auth: xodim: %w", err)
	}

	cid, err := uuid.NewV7()
	if err != nil {
		return Challenge{}, fmt.Errorf("auth: uuid: %w", err)
	}
	code, err := s.codeForEmail(email)
	if err != nil {
		return Challenge{}, err
	}
	meta := decoy
	if known {
		meta = staff.ID.String()
	}
	ttl := s.opt.OTPTTL
	if err := s.otp.SetValue(ctx, admMetaKey(cid.String()), meta, 4*ttl); err != nil {
		return Challenge{}, err
	}
	if err := s.otp.Put(ctx, admEmailKey(cid.String()), code, ttl); err != nil {
		return Challenge{}, err
	}
	if known {
		if err := s.notify.SendEmail(ctx, *staff.Email, code); err != nil {
			return Challenge{}, fmt.Errorf("auth: email: %w", err)
		}
	}
	return Challenge{ID: cid, ExpiresIn: int(ttl.Seconds()), CodeLength: otpDigits}, nil
}

func (s *Service) loadChallenge(ctx context.Context, cid uuid.UUID) (string, error) {
	meta, ok, err := s.otp.GetValue(ctx, admMetaKey(cid.String()))
	if err != nil {
		return "", err
	}
	if !ok {
		return "", apperr.OTPExpired()
	}
	return meta, nil
}

func (s *Service) AdminLoginSMS(ctx context.Context, cid uuid.UUID, emailCode, phone string) (Challenge, error) {
	meta, err := s.loadChallenge(ctx, cid)
	if err != nil {
		return Challenge{}, err
	}
	if err := s.checkCode(ctx, admEmailKey(cid.String()), emailCode); err != nil {
		return Challenge{}, err
	}
	// Email kodi to'g'ri keldi (yoki noma'lum email uchun tasodifan mos tushdi). Bundan keyingi
	// har qanday nomuvofiqlik umumiy AUTH_OTP_INVALID bilan tugaydi: sababi oshkor etilmaydi.
	staffID, perr := uuid.Parse(meta)
	if meta == decoy || perr != nil {
		return Challenge{}, apperr.OTPInvalid(0)
	}
	staff, err := s.repo.GetStaff(ctx, staffID)
	if err != nil || !staff.Active || staff.Phone != phone {
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Challenge{}, fmt.Errorf("auth: xodim: %w", err)
		}
		return Challenge{}, apperr.OTPInvalid(0)
	}

	code, err := s.codeForPhone(staff.Phone)
	if err != nil {
		return Challenge{}, err
	}
	if err := s.otp.Put(ctx, admSMSKey(cid.String()), code, s.opt.OTPTTL); err != nil {
		return Challenge{}, err
	}
	if err := s.notify.SendSMS(ctx, staff.Phone, code); err != nil {
		return Challenge{}, fmt.Errorf("auth: sms: %w", err)
	}
	return Challenge{ID: cid, ExpiresIn: int(s.opt.OTPTTL.Seconds()), CodeLength: otpDigits}, nil
}

// AdminLoginPhone — email'siz operator uchun kirish: telefon -> SMS kod (email qadamisiz).
// Noma'lum, nofaol yoki email'i bor xodim telefoni uchun soxta challenge (SMS yuborilmaydi):
// javob va keyingi verify xatolari haqiqiy holat bilan bir xil.
func (s *Service) AdminLoginPhone(ctx context.Context, phone, ip string) (Challenge, error) {
	if err := s.consume(ctx, "rl:adm:ip:"+ip, s.opt.IPLimit); err != nil {
		return Challenge{}, err
	}
	if err := s.consume(ctx, "rl:adm:phone:"+phone, s.opt.PhoneLimit); err != nil {
		return Challenge{}, err
	}

	staff, err := s.repo.GetStaffByPhone(ctx, phone)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Challenge{}, fmt.Errorf("auth: xodim: %w", err)
	}
	known := err == nil && staff.Active && staff.Email == nil

	cid, err := uuid.NewV7()
	if err != nil {
		return Challenge{}, fmt.Errorf("auth: uuid: %w", err)
	}
	code, err := s.codeForPhone(phone)
	if err != nil {
		return Challenge{}, err
	}
	meta := decoy
	if known {
		meta = staff.ID.String()
	}
	ttl := s.opt.OTPTTL
	if err := s.otp.SetValue(ctx, admMetaKey(cid.String()), meta, 4*ttl); err != nil {
		return Challenge{}, err
	}
	if err := s.otp.Put(ctx, admSMSKey(cid.String()), code, ttl); err != nil {
		return Challenge{}, err
	}
	if known {
		if err := s.notify.SendSMS(ctx, staff.Phone, code); err != nil {
			return Challenge{}, fmt.Errorf("auth: sms: %w", err)
		}
	}
	return Challenge{ID: cid, ExpiresIn: int(ttl.Seconds()), CodeLength: otpDigits}, nil
}

type AdminToken struct {
	AccessToken string
	ExpiresIn   int
}

func (s *Service) AdminLoginVerify(ctx context.Context, cid uuid.UUID, smsCode string) (AdminToken, error) {
	meta, err := s.loadChallenge(ctx, cid)
	if err != nil {
		return AdminToken{}, err
	}
	if err := s.checkCode(ctx, admSMSKey(cid.String()), smsCode); err != nil {
		return AdminToken{}, err
	}
	staffID, perr := uuid.Parse(meta)
	if perr != nil {
		return AdminToken{}, apperr.OTPExpired()
	}
	staff, err := s.repo.GetStaff(ctx, staffID)
	if err != nil || !staff.Active {
		if err != nil && !errors.Is(err, ErrNotFound) {
			return AdminToken{}, fmt.Errorf("auth: xodim: %w", err)
		}
		return AdminToken{}, apperr.OTPExpired()
	}
	_ = s.otp.Delete(ctx, admMetaKey(cid.String()))

	sid, err := uuid.NewV7()
	if err != nil {
		return AdminToken{}, fmt.Errorf("auth: uuid: %w", err)
	}
	jti, err := uuid.NewV7()
	if err != nil {
		return AdminToken{}, fmt.Errorf("auth: uuid: %w", err)
	}
	tok, _, err := s.tokens.Issue(token.IssueParams{
		Type: token.Access, Audience: token.AudAdmin, Subject: staff.ID.String(), SessionID: sid.String(),
		JTI: jti.String(), Role: string(staff.Role), Perms: staff.Role.Permissions(), TTL: s.opt.AdminTTL,
	})
	if err != nil {
		return AdminToken{}, err
	}
	if err := s.repo.TouchStaffLogin(ctx, staff.ID, time.Now()); err != nil {
		s.log.WarnContext(ctx, "last_login yangilanmadi", "staff_id", staff.ID, "err", err)
	}
	return AdminToken{AccessToken: tok, ExpiresIn: int(s.opt.AdminTTL.Seconds())}, nil
}

// AdminLogout — admin sessiyasini serverda bekor qiladi: tokendagi sid Redis blacklist'ga (bl:sid:<sid>) yoziladi.
// Faqat admin access token (aud=admin, typ=access) qabul qilinadi; client/driver tokenlari 401.
// Admin tokenida refresh yo'q, shuning uchun yozuv AdminTTL + 1 daqiqa saqlanadi (AccessTTL emas).
// Idempotent: allaqachon bekor qilingan, lekin imzosi va muddati yaroqli token bilan qayta chaqiruv ham muvaffaqiyatli.
func (s *Service) AdminLogout(ctx context.Context, raw string) error {
	c, err := s.tokens.Parse(raw, token.Access, token.AudAdmin)
	if err != nil {
		return apperr.Unauthorized()
	}
	if err := s.bl.Revoke(ctx, s.opt.AdminTTL+time.Minute, c.SID); err != nil {
		return fmt.Errorf("auth: admin logout: %w", err)
	}
	return nil
}

// EnsureBootstrapStaff — birinchi superadminni env orqali yaratadi (mavjud bo'lsa tegmaydi).
func (s *Service) EnsureBootstrapStaff(ctx context.Context, email, phone, name string) error {
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("auth: uuid: %w", err)
	}
	e := NormalizeEmail(email)
	return s.repo.EnsureStaff(ctx, Staff{
		ID: id, Email: &e, Phone: phone, FullName: name, Role: roles.Superadmin, Active: true,
	})
}

// NewStaff — ichki API (Backoffice) orqali yangi xodim login ma'lumotlari.
type NewStaff struct {
	Phone string
	Email *string // nil faqat operator uchun
	Role  roles.Role
	Name  string
}

// CreateStaff — Backoffice yaratgan xodim uchun login yozuvi. Email faqat operatorda bo'sh bo'lishi mumkin.
func (s *Service) CreateStaff(ctx context.Context, in NewStaff) (Staff, error) {
	if !in.Role.IsStaff() {
		return Staff{}, apperr.Validation(map[string]string{"role": "invalid_value"})
	}
	var email *string
	if in.Email != nil {
		e := NormalizeEmail(*in.Email)
		email = &e
	} else if in.Role != roles.Operator {
		return Staff{}, apperr.Validation(map[string]string{"email": "required"})
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Staff{}, fmt.Errorf("auth: uuid: %w", err)
	}
	st := Staff{ID: id, Email: email, Phone: in.Phone, FullName: in.Name, Role: in.Role, Active: true}
	if err := checkStaff(st); err != nil {
		return Staff{}, err
	}
	switch err := s.repo.CreateStaff(ctx, st); {
	case errors.Is(err, ErrStaffEmailTaken):
		return Staff{}, apperr.StaffEmailTaken()
	case errors.Is(err, ErrStaffPhoneTaken):
		return Staff{}, apperr.StaffPhoneTaken()
	case err != nil:
		return Staff{}, fmt.Errorf("auth: xodim yaratish: %w", err)
	}
	return st, nil
}

// StaffPatch — ichki API (Backoffice) orqali qisman yangilash: nil — o'zgarmaydi.
// Email uchun SetEmail=true va Email=nil — email olib tashlanadi (faqat operator).
type StaffPatch struct {
	Phone    *string
	SetEmail bool
	Email    *string
	Role     *roles.Role
	Name     *string
	Active   *bool
}

func (p StaffPatch) Empty() bool {
	return p.Phone == nil && !p.SetEmail && p.Role == nil && p.Name == nil && p.Active == nil
}

// UpdateStaff — Backoffice tahrirlagan xodimni identity'da ham yangilaydi (kirish ma'lumotlari shu yerda).
// Rol o'zgarishi, email/telefon band bo'lishi va "email'siz faqat operator" qoidasi natija bo'yicha tekshiriladi.
func (s *Service) UpdateStaff(ctx context.Context, id uuid.UUID, p StaffPatch) (Staff, error) {
	if p.Empty() {
		return Staff{}, apperr.Validation(map[string]string{"body": "empty"})
	}
	if p.Role != nil && !p.Role.IsStaff() {
		return Staff{}, apperr.Validation(map[string]string{"role": "invalid_value"})
	}
	st, err := s.repo.UpdateStaff(ctx, id, func(st *Staff) error {
		if p.Phone != nil {
			st.Phone = *p.Phone
		}
		if p.SetEmail {
			if p.Email == nil {
				st.Email = nil
			} else {
				e := NormalizeEmail(*p.Email)
				st.Email = &e
			}
		}
		if p.Role != nil {
			st.Role = *p.Role
		}
		if p.Name != nil {
			st.FullName = *p.Name
		}
		if p.Active != nil {
			st.Active = *p.Active
		}
		return checkStaff(*st)
	})
	switch {
	case errors.Is(err, ErrNotFound):
		return Staff{}, apperr.StaffNotFound(id.String())
	case errors.Is(err, ErrStaffEmailTaken):
		return Staff{}, apperr.StaffEmailTaken()
	case errors.Is(err, ErrStaffPhoneTaken):
		return Staff{}, apperr.StaffPhoneTaken()
	case err != nil:
		var ae *apperr.Error
		if errors.As(err, &ae) {
			return Staff{}, err
		}
		return Staff{}, fmt.Errorf("auth: xodim yangilash: %w", err)
	}
	return st, nil
}

// checkStaff — yakuniy holat qoidasi: email'siz faqat operator (DB CHECK staff_users_email_required bilan bir xil).
func checkStaff(st Staff) error {
	if st.Email == nil && st.Role != roles.Operator {
		return apperr.Validation(map[string]string{"email": "required"})
	}
	return nil
}

// LookupUser — ichki API (Backoffice): yo'lovchi/haydovchi ma'lumoti. Yo'q bo'lsa 404 NOT_FOUND.
func (s *Service) LookupUser(ctx context.Context, id uuid.UUID) (User, error) {
	u, err := s.repo.GetUser(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return User{}, apperr.NotFound()
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: foydalanuvchi: %w", err)
	}
	return u, nil
}
