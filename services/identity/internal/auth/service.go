package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/identity/internal/apperr"
	"github.com/izgo/identity/internal/cache"
	"github.com/izgo/identity/internal/events"
	"github.com/izgo/identity/internal/token"
)

const otpDigits = 6

type Options struct {
	AccessTTL, RefreshTTL, AdminTTL time.Duration
	OTPTTL, OTPResend, LimitWindow  time.Duration
	OTPMaxAttempts                  int
	PhoneLimit, IPLimit             int
}

type Service struct {
	opt    Options
	repo   Repo
	otp    *cache.OTP
	bl     *cache.Blacklist
	tokens *token.Manager
	notify Notifier
	log    *slog.Logger
	now    func() time.Time
	// onEvent — outbox'ga hodisa yozilgach relay'ni uyg'otadi (ixtiyoriy).
	onEvent func()
}

func NewService(opt Options, repo Repo, otp *cache.OTP, bl *cache.Blacklist, tokens *token.Manager, notify Notifier, log *slog.Logger) *Service {
	return &Service{opt: opt, repo: repo, otp: otp, bl: bl, tokens: tokens, notify: notify, log: log, now: time.Now}
}

func (s *Service) OnEvent(f func()) { s.onEvent = f }

type OTPResult struct{ ExpiresIn, ResendAfter int }

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	IsNewUser    *bool
}

func otpKey(phone string) string { return "otp:user:" + phone }

// RequestOTP — telefonga kod yuboradi. Tartib: cooldown (kvota yemaydi) -> IP limit -> telefon limit.
// IP limiti telefondan oldin tekshiriladi: bloklangan IP begona telefon kvotasini yemasin.
func (s *Service) RequestOTP(ctx context.Context, phone, ip string) (OTPResult, error) {
	coolKey := "otp:cool:" + phone
	if d, err := s.otp.Cooldown(ctx, coolKey); err != nil {
		return OTPResult{}, err
	} else if d > 0 {
		return OTPResult{}, apperr.RateLimited(d)
	}
	if err := s.consume(ctx, "rl:otp:ip:"+ip, s.opt.IPLimit); err != nil {
		return OTPResult{}, err
	}
	if err := s.consume(ctx, "rl:otp:phone:"+phone, s.opt.PhoneLimit); err != nil {
		return OTPResult{}, err
	}

	code, err := cache.NewCode(otpDigits)
	if err != nil {
		return OTPResult{}, err
	}
	if err := s.otp.Put(ctx, otpKey(phone), code, s.opt.OTPTTL); err != nil {
		return OTPResult{}, err
	}
	if err := s.notify.SendSMS(ctx, phone, code); err != nil {
		return OTPResult{}, fmt.Errorf("auth: sms: %w", err)
	}
	if err := s.otp.StartCooldown(ctx, coolKey, s.opt.OTPResend); err != nil {
		return OTPResult{}, err
	}
	return OTPResult{ExpiresIn: int(s.opt.OTPTTL.Seconds()), ResendAfter: int(s.opt.OTPResend.Seconds())}, nil
}

func (s *Service) consume(ctx context.Context, key string, limit int) error {
	ok, retry, err := s.otp.Hit(ctx, key, limit, s.opt.LimitWindow)
	if err != nil {
		return err
	}
	if !ok {
		return apperr.RateLimited(retry)
	}
	return nil
}

func (s *Service) checkCode(ctx context.Context, key, code string) error {
	res, left, err := s.otp.Check(ctx, key, code, s.opt.OTPMaxAttempts)
	if err != nil {
		return err
	}
	switch res {
	case cache.OTPOK:
		return nil
	case cache.OTPInvalid:
		return apperr.OTPInvalid(left)
	default:
		return apperr.OTPExpired()
	}
}

// VerifyOTP — kodni tekshiradi, foydalanuvchini topadi/yaratadi, token juftligini beradi.
func (s *Service) VerifyOTP(ctx context.Context, phone, code string, app App, traceID string) (TokenPair, error) {
	if err := s.checkCode(ctx, otpKey(phone), code); err != nil {
		return TokenPair{}, err
	}
	now := s.now()
	id, err := uuid.NewV7()
	if err != nil {
		return TokenPair{}, fmt.Errorf("auth: uuid: %w", err)
	}
	cand := User{ID: id, Phone: phone, Role: app.Role(), Settings: DefaultSettings(), CreatedAt: now}
	user, created, err := s.repo.FindOrCreateUser(ctx, cand, func(u User) (events.Event, error) {
		return events.NewUserCreated(u.ID, u.Phone, string(u.Role), u.CreatedAt, now, traceID)
	})
	if err != nil {
		return TokenPair{}, fmt.Errorf("auth: foydalanuvchi: %w", err)
	}
	if created && s.onEvent != nil {
		s.onEvent()
	}

	sid, err := uuid.NewV7()
	if err != nil {
		return TokenPair{}, fmt.Errorf("auth: uuid: %w", err)
	}
	pair, rt, err := s.mint(user, sid)
	if err != nil {
		return TokenPair{}, err
	}
	if err := s.repo.InsertRefresh(ctx, rt); err != nil {
		return TokenPair{}, fmt.Errorf("auth: refresh saqlash: %w", err)
	}
	pair.IsNewUser = &created
	return pair, nil
}

// mint — access + refresh imzolaydi (bazaga yozmaydi).
func (s *Service) mint(u User, sid uuid.UUID) (TokenPair, RefreshToken, error) {
	aud := audienceForRole(u.Role)
	accessJTI, err := uuid.NewV7()
	if err != nil {
		return TokenPair{}, RefreshToken{}, fmt.Errorf("auth: uuid: %w", err)
	}
	refreshJTI, err := uuid.NewV7()
	if err != nil {
		return TokenPair{}, RefreshToken{}, fmt.Errorf("auth: uuid: %w", err)
	}
	access, _, err := s.tokens.Issue(token.IssueParams{
		Type: token.Access, Audience: aud, Subject: u.ID.String(), SessionID: sid.String(),
		JTI: accessJTI.String(), Role: string(u.Role), Perms: u.Role.Permissions(), TTL: s.opt.AccessTTL,
	})
	if err != nil {
		return TokenPair{}, RefreshToken{}, err
	}
	refresh, rc, err := s.tokens.Issue(token.IssueParams{
		Type: token.Refresh, Audience: aud, Subject: u.ID.String(), SessionID: sid.String(),
		JTI: refreshJTI.String(), TTL: s.opt.RefreshTTL,
	})
	if err != nil {
		return TokenPair{}, RefreshToken{}, err
	}
	return TokenPair{AccessToken: access, RefreshToken: refresh, ExpiresIn: int(s.opt.AccessTTL.Seconds())},
		RefreshToken{JTI: refreshJTI, UserID: u.ID, Family: sid, ExpiresAt: rc.ExpiresAt.Time}, nil
}

// Refresh — bir martalik refresh token: ishlatilgach yangisi beriladi. Ishlatilgan token qayta
// kelsa, bu o'g'irlanish alomati: foydalanuvchining BARCHA sessiyalari bekor qilinadi.
func (s *Service) Refresh(ctx context.Context, raw string) (TokenPair, error) {
	claims, err := s.tokens.Parse(raw, token.Refresh, token.AudClient, token.AudDriver)
	if err != nil {
		return TokenPair{}, apperr.Unauthorized()
	}
	jti, err1 := uuid.Parse(claims.ID)
	sid, err2 := uuid.Parse(claims.SID)
	sub, err3 := uuid.Parse(claims.Subject)
	if err := errors.Join(err1, err2, err3); err != nil {
		return TokenPair{}, apperr.Unauthorized()
	}
	user, err := s.repo.GetUser(ctx, sub)
	if errors.Is(err, ErrNotFound) {
		return TokenPair{}, apperr.Unauthorized()
	}
	if err != nil {
		return TokenPair{}, fmt.Errorf("auth: foydalanuvchi: %w", err)
	}
	if audienceForRole(user.Role) != claims.Aud() {
		return TokenPair{}, apperr.Unauthorized()
	}

	pair, next, err := s.mint(user, sid)
	if err != nil {
		return TokenPair{}, err
	}
	outcome, owner, err := s.repo.RotateRefresh(ctx, jti, next)
	if err != nil {
		return TokenPair{}, fmt.Errorf("auth: refresh aylantirish: %w", err)
	}
	switch outcome {
	case Rotated:
		return pair, nil
	case Reused:
		s.log.WarnContext(ctx, "refresh token qayta ishlatildi: barcha sessiyalar bekor qilinadi", "user_id", owner)
		if err := s.revokeAll(ctx, owner); err != nil {
			return TokenPair{}, err
		}
	}
	return TokenPair{}, apperr.Unauthorized()
}

func (s *Service) revokeAll(ctx context.Context, userID uuid.UUID) error {
	families, err := s.repo.RevokeAllForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("auth: sessiyalarni bekor qilish: %w", err)
	}
	sids := make([]string, len(families))
	for i, f := range families {
		sids[i] = f.String()
	}
	return s.blacklist(ctx, sids...)
}

func (s *Service) blacklist(ctx context.Context, sids ...string) error {
	// Access token'lar eng ko'pi bilan AccessTTL yashaydi; zaxira uchun +1 daqiqa.
	return s.bl.Revoke(ctx, s.opt.AccessTTL+time.Minute, sids...)
}

// Authenticate — access token'ni tekshiradi (imzo, muddat, tur, audience, blacklist).
// Bu servis o'z endpointlari uchun faqat client/driver tokenlarini qabul qiladi.
func (s *Service) Authenticate(ctx context.Context, raw string) (*token.Claims, error) {
	c, err := s.tokens.Parse(raw, token.Access, token.AudClient, token.AudDriver)
	if err != nil {
		return nil, apperr.Unauthorized()
	}
	revoked, err := s.bl.IsRevoked(ctx, c.SID)
	if err != nil {
		return nil, err
	}
	if revoked {
		return nil, apperr.Unauthorized()
	}
	return c, nil
}

// Logout — joriy sessiyani (refresh oilasi va uning access tokenlari) bekor qiladi.
// Takroriy chaqiruv xato bermaydi (idempotent).
func (s *Service) Logout(ctx context.Context, access *token.Claims, rawRefresh string) error {
	rc, err := s.tokens.Parse(rawRefresh, token.Refresh, token.AudClient, token.AudDriver)
	if err != nil || rc.Subject != access.Subject || rc.SID != access.SID {
		return apperr.Unauthorized()
	}
	family, err := uuid.Parse(rc.SID)
	if err != nil {
		return apperr.Unauthorized()
	}
	if err := s.repo.RevokeFamily(ctx, family); err != nil {
		return fmt.Errorf("auth: logout: %w", err)
	}
	return s.blacklist(ctx, rc.SID)
}

func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (User, error) {
	u, err := s.repo.GetUser(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return User{}, apperr.Unauthorized()
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: profil: %w", err)
	}
	return u, nil
}

func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, first, last *string) (User, error) {
	u, err := s.repo.UpdateUserName(ctx, userID, first, last)
	if errors.Is(err, ErrNotFound) {
		return User{}, apperr.Unauthorized()
	}
	if err != nil {
		return User{}, fmt.Errorf("auth: profil yangilash: %w", err)
	}
	return u, nil
}

// UpdateSettings — berilgan maydonlarni mavjud sozlamalarga qo'shadi (merge) va to'liq sozlamalarni qaytaradi.
func (s *Service) UpdateSettings(ctx context.Context, userID uuid.UUID, upd SettingsUpdate) (Settings, error) {
	u, err := s.GetProfile(ctx, userID)
	if err != nil {
		return Settings{}, err
	}
	merged := u.Settings.Apply(upd)
	if err := s.repo.UpdateUserSettings(ctx, userID, merged); err != nil {
		return Settings{}, fmt.Errorf("auth: sozlamalar: %w", err)
	}
	return merged, nil
}
