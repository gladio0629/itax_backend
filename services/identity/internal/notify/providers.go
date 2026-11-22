// Package notify — OTP kodlarini yetkazuvchi tashqi provayderlar: SMS (Eskiz.uz) va email (Resend).
// Maxfiy qiymatlar faqat muhitdan keladi (SMS_PROVIDER_EMAIL/SMS_PROVIDER_PASSWORD, EMAIL_API_KEY);
// ular va provayder tokeni kodda va logda hech qachon ko'rinmaydi.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Provayder javob kodlari bo'yicha xatolar (errors.Is bilan tekshiriladi).
var (
	ErrUnauthorized        = errors.New("provider: avtorizatsiya xatosi (401)")
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
