// Package notify — OTP kodlarini yetkazuvchi tashqi provayderlar: SMS (smsxabarnoma.uz) va email (Resend).
// Kalitlar faqat muhitdan keladi (SMS_PROVIDER_KEY, EMAIL_API_KEY); kodda va logda hech qachon ko'rinmaydi.
package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Provayder javob kodlari bo'yicha xatolar (errors.Is bilan tekshiriladi).
var (
	ErrUnauthorized        = errors.New("provider: API kalit noto'g'ri (401)")
	ErrInsufficientBalance = errors.New("provider: balans yetarli emas (402)")
	ErrRateLimited         = errors.New("provider: limit tugagan (429)")
	ErrInvalidPhone        = errors.New("notify: telefon 998XXXXXXXXX formatida bo'lishi kerak")
)

// ProviderError — provayder 2xx bo'lmagan javob qaytardi. Javob tanasi xatoga kiritilmaydi
// (unda maxfiy yoki keraksiz ma'lumot bo'lishi mumkin) — faqat status.
type ProviderError struct {
	Provider string
	Status   int
	Err      error // sentinel (ErrUnauthorized, ...) yoki nil
}

func (e *ProviderError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Provider, e.Err)
	}
	return fmt.Sprintf("%s: kutilmagan status %d", e.Provider, e.Status)
}

func (e *ProviderError) Unwrap() error { return e.Err }

func statusErr(provider string, status int) error {
	var sentinel error
	switch status {
	case http.StatusUnauthorized:
		sentinel = ErrUnauthorized
	case http.StatusPaymentRequired:
		sentinel = ErrInsufficientBalance
	case http.StatusTooManyRequests:
		sentinel = ErrRateLimited
	}
	return &ProviderError{Provider: provider, Status: status, Err: sentinel}
}

// maxBody — provayder javobidan o'qiladigan eng ko'p bayt.
const maxBody = 64 << 10

// DefaultTimeout — provayderga bitta so'rov uchun. Identity HTTP WriteTimeout (15s) dan ancha kam.
const DefaultTimeout = 8 * time.Second

var phoneRe = regexp.MustCompile(`^998[0-9]{9}$`)

// NormalizePhone — "+998901234567" -> "998901234567". Bo'shliq/chiziqcha olib tashlanadi; 998 bilan boshlanmasa xato.
func NormalizePhone(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "+")
	p = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(p)
	if !phoneRe.MatchString(p) {
		return "", ErrInvalidPhone
	}
	return p, nil
}

// Ref — tashqi identifikator uchun barqaror, lekin telefonni oshkor qilmaydigan qisqa qiymat.
func Ref(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:6])
}

// SMSXabarnoma — https://smsxabarnoma.uz/api/v1 mijoz.
type SMSXabarnoma struct {
	BaseURL string // SMS_PROVIDER_URL, masalan https://smsxabarnoma.uz/api/v1
	APIKey  string // SMS_PROVIDER_KEY
	HTTP    *http.Client
}

type smsRequest struct {
	Phone          string `json:"phone"`
	Text           string `json:"text"`
	ExternalID     string `json:"externalId"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// SMSResult — provayder javobi. Cost turi hujjatda satr ("60"), shuning uchun xom saqlanadi.
type SMSResult struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Parts  int             `json:"parts"`
	Cost   json.RawMessage `json:"cost"`
}

// Send — bitta SMS. externalID idempotencyKey sifatida ham ishlatiladi: bir xil kalit bilan qayta
// yuborilgan so'rov provayderda ikkinchi SMS yaratmaydi.
func (c *SMSXabarnoma) Send(ctx context.Context, phone, text, externalID string) (SMSResult, error) {
	norm, err := NormalizePhone(phone)
	if err != nil {
		return SMSResult{}, err
	}
	body, err := json.Marshal(smsRequest{Phone: norm, Text: text, ExternalID: externalID, IdempotencyKey: externalID})
	if err != nil {
		return SMSResult{}, fmt.Errorf("smsxabarnoma: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/sms/send", bytes.NewReader(body))
	if err != nil {
		return SMSResult{}, fmt.Errorf("smsxabarnoma: %w", err)
	}
	req.Header.Set("X-API-Key", c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	var out SMSResult
	if err := do(c.client(), req, "smsxabarnoma", &out); err != nil {
		return SMSResult{}, err
	}
	return out, nil
}

func (c *SMSXabarnoma) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: DefaultTimeout}
}

// ResendBaseURL — Resend API manzili (testlarda almashtiriladi).
const ResendBaseURL = "https://api.resend.com"

// Resend — https://resend.com email API mijozi.
type Resend struct {
	BaseURL string // bo'sh bo'lsa ResendBaseURL
	APIKey  string // EMAIL_API_KEY
	From    string // EMAIL_FROM
	HTTP    *http.Client
}

type resendRequest struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

// Send — bitta oddiy matnli email; muvaffaqiyatda provayder id sini qaytaradi.
func (c *Resend) Send(ctx context.Context, to, subject, text string) (string, error) {
	body, err := json.Marshal(resendRequest{From: c.From, To: to, Subject: subject, Text: text})
	if err != nil {
		return "", fmt.Errorf("resend: %w", err)
	}
	base := c.BaseURL
	if base == "" {
		base = ResendBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/emails", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("resend: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	var out struct {
		ID string `json:"id"`
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	if err := do(client, req, "resend", &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// do — so'rovni yuboradi; 2xx bo'lmasa ProviderError. 2xx javob tanasi o'qilmasa ham xabar qabul
// qilingan hisoblanadi (qayta yuborish foydalanuvchiga ikkinchi kod jo'natardi).
func do(client *http.Client, req *http.Request, provider string, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: so'rov: %w", provider, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode/100 != 2 {
		return statusErr(provider, resp.StatusCode)
	}
	_ = json.Unmarshal(data, out)
	return nil
}

// ExternalID — "<prefix>-<ref>-<unix ms>".
func ExternalID(prefix, ref string, now time.Time) string {
	return prefix + "-" + ref + "-" + strconv.FormatInt(now.UnixMilli(), 10)
}
