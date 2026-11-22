package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Eskiz.uz SMS API (https://notify.eskiz.uz/api). Hujjat: https://documenter.getpostman.com/view/663428/RzfmES4z
//
//	POST  /auth/login            form-data: email, password      -> {"data":{"token":"..."}}
//	POST  /message/sms/send      Bearer + form-data: mobile_phone, message, from
//
// Token 30 kun amal qiladi: Redis'da EskizTokenTTL (29 kun) saqlanadi va har so'rovda login qilinmaydi.
// Token muddati tugasa (yoki bekor qilinsa) send 401 qaytaradi — shunda bir marta qayta login va qayta yuborish.
const (
	EskizBaseURL  = "https://notify.eskiz.uz/api"
	EskizFrom     = "4546" // Eskiz standart sender ID
	EskizTokenTTL = 29 * 24 * time.Hour

	// EskizSendTimeout — bitta Send uchun umumiy chegara (send + login + qayta send). Identity HTTP
	// WriteTimeout (15s) dan kam bo'lishi kerak.
	EskizSendTimeout = 12 * time.Second
)

// ErrEmptyToken — login 2xx qaytardi, lekin token bo'sh (keshga yozilmaydi).
var ErrEmptyToken = errors.New("eskiz: login javobida token yo'q")

// TokenStore — provayder tokeni keshi (production'da Redis: cache.EskizToken). Get: yo'q bo'lsa "" va nil.
type TokenStore interface {
	Get(ctx context.Context) (string, error)
	Set(ctx context.Context, token string, ttl time.Duration) error
	Del(ctx context.Context) error
}

// Eskiz — Eskiz.uz SMS mijozi.
type Eskiz struct {
	BaseURL  string // SMS_PROVIDER_URL; bo'sh bo'lsa EskizBaseURL
	Email    string // SMS_PROVIDER_EMAIL
	Password string // SMS_PROVIDER_PASSWORD
	From     string // bo'sh bo'lsa EskizFrom
	Tokens   TokenStore
	HTTP     *http.Client

	loginMu sync.Mutex // bir jarayonda parallel loginlarni bittaga qisqartiradi
}

// SMSResult — Eskiz send javobi: {"id":"...","status":"waiting","message":"Waiting for SMS provider"}.
type SMSResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Send — bitta SMS. Kesh bo'sh bo'lsa login; send 401 bo'lsa tokenni yangilab aynan bir marta qayta yuboradi.
// Timeout/5xx da qayta yuborilmaydi: foydalanuvchiga ikkinchi kod borib qolishi mumkin.
func (c *Eskiz) Send(ctx context.Context, phone, text string) (SMSResult, error) {
	norm, err := NormalizePhone(phone)
	if err != nil {
		return SMSResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, EskizSendTimeout)
	defer cancel()

	token, err := c.token(ctx, "")
	if err != nil {
		return SMSResult{}, err
	}
	res, err := c.send(ctx, token, norm, text)
	if !errors.Is(err, ErrUnauthorized) {
		return res, err
	}
	// Token eskirgan: yangisini olib, bir marta qayta urinamiz.
	if token, err = c.token(ctx, token); err != nil {
		return SMSResult{}, err
	}
	return c.send(ctx, token, norm, text)
}

// token — keshdagi tokenni qaytaradi; yo'q bo'lsa yoki u `stale` ga teng bo'lsa (401 olgan) qayta login.
// Kesh xatolari SMS ni to'xtatmaydi — login qilinadi.
func (c *Eskiz) token(ctx context.Context, stale string) (string, error) {
	if t, _ := c.Tokens.Get(ctx); t != "" && t != stale {
		return t, nil
	}
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	// Kutayotgan paytda boshqa goroutine yangilagan bo'lishi mumkin.
	if t, _ := c.Tokens.Get(ctx); t != "" && t != stale {
		return t, nil
	}
	if stale != "" {
		_ = c.Tokens.Del(ctx)
	}
	t, err := c.login(ctx)
	if err != nil {
		return "", err
	}
	_ = c.Tokens.Set(ctx, t, EskizTokenTTL)
	return t, nil
}

func (c *Eskiz) login(ctx context.Context) (string, error) {
	req, err := c.formRequest(ctx, "/auth/login", map[string]string{"email": c.Email, "password": c.Password})
	if err != nil {
		return "", err
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := do(c.client(), req, "eskiz login", &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Data.Token) == "" {
		return "", ErrEmptyToken
	}
	return out.Data.Token, nil
}

func (c *Eskiz) send(ctx context.Context, token, phone, text string) (SMSResult, error) {
	from := c.From
	if from == "" {
		from = EskizFrom
	}
	req, err := c.formRequest(ctx, "/message/sms/send", map[string]string{"mobile_phone": phone, "message": text, "from": from})
	if err != nil {
		return SMSResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var out SMSResult
	if err := do(c.client(), req, "eskiz", &out); err != nil {
		return SMSResult{}, err
	}
	return out, nil
}

// formRequest — multipart/form-data POST (maydonlar tartibi barqaror bo'lishi shart emas).
func (c *Eskiz) formRequest(ctx context.Context, path string, fields map[string]string) (*http.Request, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("eskiz: %w", err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("eskiz: %w", err)
	}
	base := c.BaseURL
	if base == "" {
		base = EskizBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, &buf)
	if err != nil {
		return nil, fmt.Errorf("eskiz: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Eskiz) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: DefaultTimeout}
}

// MemTokenStore — jarayon ichidagi kesh (Redis berilmaganda va testlarda). TTL e'tiborga olinadi.
type MemTokenStore struct {
	mu    sync.Mutex
	token string
	exp   time.Time
}

func (m *MemTokenStore) Get(context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token == "" || time.Now().After(m.exp) {
		return "", nil
	}
	return m.token, nil
}

func (m *MemTokenStore) Set(_ context.Context, token string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token, m.exp = token, time.Now().Add(ttl)
	return nil
}

func (m *MemTokenStore) Del(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token = ""
	return nil
}
