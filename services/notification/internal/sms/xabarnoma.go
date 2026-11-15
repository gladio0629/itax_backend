package sms

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Rejimlar (SMS_MODE).
const (
	ModeMock = "mock"
	ModeReal = "real"
)

// Provayder javob kodlari bo'yicha xatolar (errors.Is bilan tekshiriladi).
var (
	ErrUnauthorized        = errors.New("sms provider: API kalit noto'g'ri (401)")
	ErrInsufficientBalance = errors.New("sms provider: balans yetarli emas (402)")
	ErrRateLimited         = errors.New("sms provider: limit tugagan (429)")
	ErrInvalidPhone        = errors.New("sms: telefon 998XXXXXXXXX formatida bo'lishi kerak")
)

// ProviderError — provayder 2xx bo'lmagan javob qaytardi (javob tanasi xatoga kiritilmaydi).
type ProviderError struct {
	Status int
	Err    error // sentinel yoki nil
}

func (e *ProviderError) Error() string {
	if e.Err != nil {
		return "smsxabarnoma: " + e.Err.Error()
	}
	return fmt.Sprintf("smsxabarnoma: kutilmagan status %d", e.Status)
}

func (e *ProviderError) Unwrap() error { return e.Err }

const (
	maxBody = 64 << 10
	// DefaultTimeout — provayderga bitta so'rov uchun.
	DefaultTimeout = 8 * time.Second
)

var providerPhoneRe = regexp.MustCompile(`^998[0-9]{9}$`)

// NormalizePhone — "+998901234567" -> "998901234567"; 998 bilan boshlanmasa xato.
func NormalizePhone(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "+")
	p = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(p)
	if !providerPhoneRe.MatchString(p) {
		return "", ErrInvalidPhone
	}
	return p, nil
}

// UseReal — real rejim faqat SMS_MODE=real VA kalit bo'sh bo'lmaganda.
func UseReal(mode, key string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), ModeReal) && strings.TrimSpace(key) != ""
}

// Xabarnoma — smsxabarnoma.uz orqali yuboruvchi (Sender).
type Xabarnoma struct {
	BaseURL string // SMS_PROVIDER_URL, masalan https://smsxabarnoma.uz/api/v1
	APIKey  string // SMS_PROVIDER_KEY
	HTTP    *http.Client
	Log     *slog.Logger
	Now     func() time.Time
}

type xabarnomaRequest struct {
	Phone          string `json:"phone"`
	Text           string `json:"text"`
	ExternalID     string `json:"externalId"`
	IdempotencyKey string `json:"idempotencyKey"`
}

type xabarnomaResponse struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Parts  int             `json:"parts"`
	Cost   json.RawMessage `json:"cost"` // hujjatda satr ("60")
}

func (x Xabarnoma) Send(ctx context.Context, phone, text string) error {
	norm, err := NormalizePhone(phone)
	if err != nil {
		return err
	}
	now := time.Now
	if x.Now != nil {
		now = x.Now
	}
	h := sha256.Sum256([]byte(norm))
	extID := "sms-" + hex.EncodeToString(h[:6]) + "-" + strconv.FormatInt(now().UnixMilli(), 10)

	body, err := json.Marshal(xabarnomaRequest{Phone: norm, Text: text, ExternalID: extID, IdempotencyKey: extID})
	if err != nil {
		return fmt.Errorf("smsxabarnoma: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(x.BaseURL, "/")+"/sms/send", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("smsxabarnoma: %w", err)
	}
	req.Header.Set("X-API-Key", x.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := x.HTTP
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("smsxabarnoma: so'rov: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode/100 != 2 {
		var sentinel error
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			sentinel = ErrUnauthorized
		case http.StatusPaymentRequired:
			sentinel = ErrInsufficientBalance
		case http.StatusTooManyRequests:
			sentinel = ErrRateLimited
		}
		perr := &ProviderError{Status: resp.StatusCode, Err: sentinel}
		x.logger().WarnContext(ctx, "SMS yuborilmadi", "phone", MaskPhone(phone), "external_id", extID, "err", perr)
		return perr
	}
	// 2xx: xabar qabul qilingan — tana o'qilmasa ham muvaffaqiyat (qayta yuborish ikkinchi SMS bo'lardi).
	var out xabarnomaResponse
	_ = json.Unmarshal(data, &out)
	x.logger().InfoContext(ctx, "SMS yuborildi", "phone", MaskPhone(phone), "external_id", extID,
		"provider_id", out.ID, "status", out.Status, "parts", out.Parts)
	return nil
}

func (x Xabarnoma) logger() *slog.Logger {
	if x.Log != nil {
		return x.Log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
