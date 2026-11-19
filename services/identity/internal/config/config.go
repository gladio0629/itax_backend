// Package config — servis sozlamalari (faqat muhit o'zgaruvchilaridan).
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/izgo/identity/internal/auth"
	"github.com/kelseyhightower/envconfig"
)

// Config — barcha qiymatlar muhitdan o'qiladi; maxfiy qiymatlar kodda yo'q.
type Config struct {
	Port string `envconfig:"PORT" default:"8010"`

	// PostgreSQL (identity_db). Alohida maydonlar — parolni URL'ga kodlash shart emas.
	DBHost     string `envconfig:"DB_HOST" default:"postgres"`
	DBPort     int    `envconfig:"DB_PORT" default:"5432"`
	DBUser     string `envconfig:"DB_USER" default:"identity_svc"`
	DBPassword string `envconfig:"DB_PASSWORD"`
	DBName     string `envconfig:"DB_NAME" default:"identity_db"`
	DBSSLMode  string `envconfig:"DB_SSLMODE" default:"disable"`

	RedisURL string `envconfig:"REDIS_URL" required:"true"`
	NATSURL  string `envconfig:"NATS_URL" required:"true"`

	// JWT: Ed25519 kalitlari (PEM, PKCS#8 / PKIX) fayl yo'li orqali.
	JWTPrivateKeyPath string `envconfig:"JWT_PRIVATE_KEY_PATH" required:"true"`
	JWTPublicKeyPath  string `envconfig:"JWT_PUBLIC_KEY_PATH" required:"true"`
	JWTIssuer         string `envconfig:"JWT_ISSUER" default:"izgo-identity"`

	AccessTTL  time.Duration `envconfig:"ACCESS_TOKEN_TTL" default:"15m"`
	RefreshTTL time.Duration `envconfig:"REFRESH_TOKEN_TTL" default:"168h"` // 7 kun
	// Admin tokenida refresh yo'q (openapi: AdminToken), shuning uchun 15 daqiqa amalda yaroqsiz.
	AdminTTL time.Duration `envconfig:"ADMIN_TOKEN_TTL" default:"1h"`

	// OTP (F-04)
	OTPTTL         time.Duration `envconfig:"OTP_TTL" default:"3m"`
	OTPResend      time.Duration `envconfig:"OTP_RESEND_AFTER" default:"60s"`
	OTPMaxAttempts int           `envconfig:"OTP_MAX_ATTEMPTS" default:"3"`
	OTPPhoneLimit  int           `envconfig:"OTP_PHONE_LIMIT" default:"5"`
	OTPIPLimit     int           `envconfig:"OTP_IP_LIMIT" default:"20"`
	OTPLimitWindow time.Duration `envconfig:"OTP_LIMIT_WINDOW" default:"1h"`

	// Ichki API (/internal/*) uchun umumiy sir: X-Internal-Token sarlavhasi. Vergul bilan bir nechta qiymat
	// (almashtirish davrida eski va yangisi birga ishlaydi). Har biri kamida 16 belgi.
	InternalTokens []string `envconfig:"INTERNAL_TOKENS" required:"true"`

	// Birinchi superadmin (ixtiyoriy). Faqat email + telefon: kirish baribir OTP orqali.
	AdminBootstrapEmail string `envconfig:"ADMIN_BOOTSTRAP_EMAIL"`
	AdminBootstrapPhone string `envconfig:"ADMIN_BOOTSTRAP_PHONE"`
	AdminBootstrapName  string `envconfig:"ADMIN_BOOTSTRAP_NAME" default:"Superadmin"`

	// SMS provayder (smsxabarnoma.uz). SMS_MODE=real va kalit bo'lsa yuboriladi, aks holda logga (mock).
	SMSMode        string `envconfig:"SMS_MODE" default:"mock"`
	SMSProviderURL string `envconfig:"SMS_PROVIDER_URL" default:"https://smsxabarnoma.uz/api/v1"`
	SMSProviderKey string `envconfig:"SMS_PROVIDER_KEY"`

	// Email provayder (Resend) — admin kirish kodi. EMAIL_MODE=real va kalit bo'lsa yuboriladi, aks holda logga.
	EmailMode     string `envconfig:"EMAIL_MODE" default:"mock"`
	EmailProvider string `envconfig:"EMAIL_PROVIDER" default:"resend"`
	EmailAPIKey   string `envconfig:"EMAIL_API_KEY"`
	EmailFrom     string `envconfig:"EMAIL_FROM" default:"onboarding@resend.dev"`

	// AppEnv — muhit nomi. production (yoki prod) bo'lsa DEV_MODE yoqib bo'lmaydi.
	AppEnv string `envconfig:"APP_ENV" default:"development"`

	// Dev rejim (FAQAT lokal/test): ro'yxatdagi telefon/email'lar uchun OTP tasodifiy emas, DEV_OTP_CODE bo'ladi.
	// DEV_MODE=true bo'lsa APP_ENV=production, SMS_MODE=real yoki EMAIL_MODE=real bilan servis ishga tushmaydi.
	DevMode       bool     `envconfig:"DEV_MODE" default:"false"`
	DevOTPCode    string   `envconfig:"DEV_OTP_CODE"`
	DevTestPhones []string `envconfig:"DEV_TEST_PHONES"`
	DevTestEmails []string `envconfig:"DEV_TEST_EMAILS"`

	// Review (App Store / Google Play tekshiruvchisi): production'da ham ishlaydi, DEV_MODE ga bog'liq emas.
	// Qiymatlar env'da emas, FAYLDA (up.sh -> scripts/gen-secrets.sh .env dan secrets/svc/identity/ ga yozadi).
	// Ikkala fayl yo'q — o'chirilgan; bittasi bor, o'qib bo'lmaydi yoki format noto'g'ri — servis ishga tushmaydi.
	ReviewOTPCodeFile string `envconfig:"REVIEW_OTP_CODE_FILE"`
	ReviewPhonesFile  string `envconfig:"REVIEW_PHONES_FILE"`
	// Fayllardan o'qilgan qiymatlar (Load to'ldiradi).
	ReviewOTPCode string   `ignored:"true"`
	ReviewPhones  []string `ignored:"true"`
}

var (
	devPhoneRe = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`) // E.164
	digitsRe   = regexp.MustCompile(`^[0-9]+$`)
)

// Load — muhitdan o'qiydi va tekshiradi.
func Load() (Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := c.loadReview(); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

func (c Config) validate() error {
	var errs []error
	for name, v := range map[string]string{
		"DB_PASSWORD": c.DBPassword, "REDIS_URL": c.RedisURL, "NATS_URL": c.NATSURL,
		"JWT_PRIVATE_KEY_PATH": c.JWTPrivateKeyPath, "JWT_PUBLIC_KEY_PATH": c.JWTPublicKeyPath,
	} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s bo'sh bo'lmasligi kerak", name))
		}
	}
	for name, v := range map[string]time.Duration{
		"ACCESS_TOKEN_TTL": c.AccessTTL, "REFRESH_TOKEN_TTL": c.RefreshTTL, "ADMIN_TOKEN_TTL": c.AdminTTL,
		"OTP_TTL": c.OTPTTL, "OTP_LIMIT_WINDOW": c.OTPLimitWindow,
	} {
		if v <= 0 {
			errs = append(errs, fmt.Errorf("%s musbat bo'lishi kerak", name))
		}
	}
	for name, v := range map[string]int{
		"OTP_MAX_ATTEMPTS": c.OTPMaxAttempts, "OTP_PHONE_LIMIT": c.OTPPhoneLimit, "OTP_IP_LIMIT": c.OTPIPLimit,
	} {
		if v <= 0 {
			errs = append(errs, fmt.Errorf("%s musbat bo'lishi kerak", name))
		}
	}
	if len(c.InternalTokens) == 0 {
		errs = append(errs, errors.New("INTERNAL_TOKENS bo'sh bo'lmasligi kerak"))
	}
	for _, t := range c.InternalTokens {
		if len(strings.TrimSpace(t)) < 16 {
			errs = append(errs, errors.New("INTERNAL_TOKENS: har bir qiymat kamida 16 belgi bo'lishi kerak"))
			break
		}
	}
	if c.DevMode {
		errs = append(errs, c.validateDev()...)
	}
	errs = append(errs, c.validateReview()...)
	if (c.AdminBootstrapEmail == "") != (c.AdminBootstrapPhone == "") {
		errs = append(errs, errors.New("ADMIN_BOOTSTRAP_EMAIL va ADMIN_BOOTSTRAP_PHONE birga berilishi kerak"))
	}
	for name, v := range map[string]string{"SMS_MODE": c.SMSMode, "EMAIL_MODE": c.EmailMode} {
		if !validMode(v) {
			errs = append(errs, fmt.Errorf("%s faqat mock yoki real bo'lishi mumkin", name))
		}
	}
	if isReal(c.SMSMode) && strings.TrimSpace(c.SMSProviderURL) == "" {
		errs = append(errs, errors.New("SMS_MODE=real uchun SMS_PROVIDER_URL kerak"))
	}
	if isReal(c.EmailMode) {
		if !strings.EqualFold(strings.TrimSpace(c.EmailProvider), "resend") {
			errs = append(errs, errors.New("EMAIL_PROVIDER: hozircha faqat resend qo'llab-quvvatlanadi"))
		}
		if strings.TrimSpace(c.EmailFrom) == "" {
			errs = append(errs, errors.New("EMAIL_MODE=real uchun EMAIL_FROM kerak"))
		}
	}
	return errors.Join(errs...)
}

// validateDev — DEV_MODE=true uchun xavfsizlik va format tekshiruvlari (fail closed: xato bo'lsa servis ishga tushmaydi).
func (c Config) validateDev() []error {
	var errs []error
	if env := strings.ToLower(strings.TrimSpace(c.AppEnv)); env == "production" || env == "prod" {
		errs = append(errs, errors.New("DEV_MODE=true APP_ENV=production bilan taqiqlangan"))
	}
	if isReal(c.SMSMode) {
		errs = append(errs, errors.New("DEV_MODE=true SMS_MODE=real bilan taqiqlangan"))
	}
	if isReal(c.EmailMode) {
		errs = append(errs, errors.New("DEV_MODE=true EMAIL_MODE=real bilan taqiqlangan"))
	}
	if code := strings.TrimSpace(c.DevOTPCode); len(code) != auth.OTPLength || !digitsRe.MatchString(code) {
		errs = append(errs, fmt.Errorf("DEV_MODE=true uchun DEV_OTP_CODE aynan %d ta raqam bo'lishi kerak", auth.OTPLength))
	}
	for _, p := range cleanList(c.DevTestPhones) {
		if !devPhoneRe.MatchString(p) {
			errs = append(errs, errors.New("DEV_TEST_PHONES: har bir qiymat E.164 formatida bo'lishi kerak (+998901234567)"))
			break
		}
	}
	for _, e := range cleanList(c.DevTestEmails) {
		if !strings.Contains(e, "@") {
			errs = append(errs, errors.New("DEV_TEST_EMAILS: noto'g'ri email"))
			break
		}
	}
	return errs
}

// DevOTP — auth.Options.Dev uchun. DEV_MODE=false bo'lsa nol qiymat (ro'yxatlar va kod e'tiborsiz qoldiriladi).
func (c Config) DevOTP() auth.DevOTP {
	if !c.DevMode {
		return auth.DevOTP{}
	}
	d := auth.DevOTP{Code: strings.TrimSpace(c.DevOTPCode), Phones: map[string]struct{}{}, Emails: map[string]struct{}{}}
	for _, p := range cleanList(c.DevTestPhones) {
		d.Phones[p] = struct{}{}
	}
	for _, e := range cleanList(c.DevTestEmails) {
		d.Emails[auth.NormalizeEmail(e)] = struct{}{}
	}
	return d
}

// loadReview — REVIEW_* fayllarini o'qiydi. Fayl yo'q bo'lsa qiymat bo'sh (katalog mount qilingan, fayl
// yaratilmagan holat). Boshqa o'qish xatosi (masalan ruxsat) — servis ishga tushmaydi.
func (c *Config) loadReview() error {
	code, err := readOptionalFile("REVIEW_OTP_CODE_FILE", c.ReviewOTPCodeFile)
	if err != nil {
		return err
	}
	phones, err := readOptionalFile("REVIEW_PHONES_FILE", c.ReviewPhonesFile)
	if err != nil {
		return err
	}
	c.ReviewOTPCode = strings.TrimSpace(code)
	c.ReviewPhones = cleanList(strings.FieldsFunc(phones, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }))
	return nil
}

func readOptionalFile(name, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s o'qilmadi: %w", name, err)
	}
	return string(b), nil
}

// validateReview — review qiymatlari ikkalasi birga bo'lishi va formati to'g'ri bo'lishi shart (fail closed).
func (c Config) validateReview() []error {
	hasCode, hasPhones := c.ReviewOTPCode != "", len(c.ReviewPhones) > 0
	if !hasCode && !hasPhones {
		return nil
	}
	var errs []error
	if hasCode != hasPhones {
		errs = append(errs, errors.New("REVIEW_OTP_CODE va REVIEW_PHONES birga berilishi kerak"))
	}
	if hasCode && (len(c.ReviewOTPCode) != auth.OTPLength || !digitsRe.MatchString(c.ReviewOTPCode)) {
		errs = append(errs, fmt.Errorf("REVIEW_OTP_CODE aynan %d ta raqam bo'lishi kerak", auth.OTPLength))
	}
	for _, p := range c.ReviewPhones {
		if !devPhoneRe.MatchString(p) {
			errs = append(errs, errors.New("REVIEW_PHONES: har bir qiymat E.164 formatida bo'lishi kerak (+998901234567)"))
			break
		}
	}
	return errs
}

// ReviewOTP — auth.Options.Review uchun. Load muvaffaqiyatli bo'lsa qiymatlar tekshirilgan.
func (c Config) ReviewOTP() auth.ReviewOTP {
	if c.ReviewOTPCode == "" || len(c.ReviewPhones) == 0 {
		return auth.ReviewOTP{}
	}
	r := auth.ReviewOTP{Code: c.ReviewOTPCode, Phones: map[string]struct{}{}}
	for _, p := range c.ReviewPhones {
		r.Phones[p] = struct{}{}
	}
	return r
}

// cleanList — vergul bilan ajratilgan qiymatlar: bo'sh joylar olib tashlanadi, bo'sh elementlar tashlab yuboriladi.
func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// validMode — bo'sh qiymat mock deb olinadi (compose'da `${SMS_MODE:-}` bo'sh satr berishi mumkin).
func validMode(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "mock", "real":
		return true
	}
	return false
}

func isReal(v string) bool { return strings.EqualFold(strings.TrimSpace(v), "real") }
