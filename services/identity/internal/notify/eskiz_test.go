package notify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	eskizEmail    = "sms@example.com"
	eskizPassword = "test-good-password"
)

// fakeEskiz — notify.eskiz.uz o'rnida. Login noto'g'ri email/parolda 401; send faqat amaldagi tokenni
// qabul qiladi (boshqasi — 401, eskirgan token kabi). Har login yangi token beradi.
type fakeEskiz struct {
	mu        sync.Mutex
	srv       *httptest.Server
	valid     string // amaldagi token
	issued    int
	logins    int
	sends     int
	lastAuth  string
	lastForm  map[string]string
	lastCType string
	sendCode  int // 0 bo'lmasa send (token to'g'ri bo'lganda) shu statusni qaytaradi
}

func newFakeEskiz(t *testing.T) *fakeEskiz {
	t.Helper()
	f := &fakeEskiz{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeEskiz) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	form := map[string]string{}
	for k, v := range r.MultipartForm.Value {
		form[k] = v[0]
	}
	switch r.URL.Path {
	case "/api/auth/login":
		f.logins++
		if form["email"] != eskizEmail || form["password"] != eskizPassword {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Invalid credentials"}`))
			return
		}
		f.issued++
		f.valid = fmt.Sprintf("tok-%d", f.issued)
		_, _ = w.Write([]byte(`{"message":"token_generated","data":{"token":"` + f.valid + `"},"token_type":"bearer"}`))
	case "/api/message/sms/send":
		f.sends++
		f.lastAuth = r.Header.Get("Authorization")
		f.lastForm = form
		f.lastCType = r.Header.Get("Content-Type")
		if f.valid == "" || f.lastAuth != "Bearer "+f.valid {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if f.sendCode != 0 {
			w.WriteHeader(f.sendCode)
			return
		}
		_, _ = w.Write([]byte(`{"id":"sms-1","status":"waiting","message":"Waiting for SMS provider"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// expire — Eskiz tomonida tokenni bekor qiladi (30 kun o'tgandek).
func (f *fakeEskiz) expire() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.valid = "revoked"
}

func (f *fakeEskiz) counts() (logins, sends int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins, f.sends
}

// ttlStore — MemTokenStore + oxirgi Set TTL va Set soni.
type ttlStore struct {
	MemTokenStore
	lastTTL time.Duration
	sets    int
}

func (s *ttlStore) Set(ctx context.Context, token string, ttl time.Duration) error {
	s.lastTTL = ttl
	s.sets++
	return s.MemTokenStore.Set(ctx, token, ttl)
}

func eskizCfg(f *fakeEskiz, store TokenStore) Config {
	return Config{SMSMode: "real", SMSProviderURL: f.srv.URL + "/api/", SMSProviderEmail: eskizEmail,
		SMSProviderPassword: eskizPassword, SMSTokens: store}
}

// Token olish: kesh bo'sh -> login (email+parol form-data) -> token keshga 29 kunga yoziladi.
func TestEskizLoginCachesToken(t *testing.T) {
	f := newFakeEskiz(t)
	store := &ttlStore{}
	n := New(eskizCfg(f, store), &recFallback{}, quietLog())
	if !n.SMSReal() {
		t.Fatal("real rejim tanlanmadi")
	}
	if err := n.SendSMS(context.Background(), "+998901234567", "123456"); err != nil {
		t.Fatalf("SendSMS: %v", err)
	}
	if logins, _ := f.counts(); logins != 1 {
		t.Fatalf("logins = %d, want 1", logins)
	}
	if got, _ := store.Get(context.Background()); got != "tok-1" {
		t.Fatalf("keshdagi token = %q", got)
	}
	if store.lastTTL != 29*24*time.Hour {
		t.Fatalf("token TTL = %v, want 29 kun", store.lastTTL)
	}
}

// SMS yuborish: Bearer token, form-data, telefon + siz, from=4546, matn.
func TestEskizSendRequestShape(t *testing.T) {
	f := newFakeEskiz(t)
	n := New(eskizCfg(f, nil), &recFallback{}, quietLog())
	if err := n.SendSMS(context.Background(), " +998 90 123-45-67", "123456"); err != nil {
		t.Fatalf("SendSMS: %v", err)
	}
	if f.lastAuth != "Bearer tok-1" {
		t.Errorf("Authorization = %q", f.lastAuth)
	}
	if !strings.HasPrefix(f.lastCType, "multipart/form-data") {
		t.Errorf("Content-Type = %q", f.lastCType)
	}
	want := map[string]string{"mobile_phone": "998901234567", "message": "NURGROUP platformasida parolni tiklash uchun tasdiqlash kodi: 123456", "from": "4546"}
	for k, v := range want {
		if f.lastForm[k] != v {
			t.Errorf("%s = %q, want %q", k, f.lastForm[k], v)
		}
	}
}

// Keshdagi token qayta ishlatiladi: keyingi SMS larda login yo'q.
func TestEskizReusesCachedToken(t *testing.T) {
	f := newFakeEskiz(t)
	n := New(eskizCfg(f, nil), &recFallback{}, quietLog())
	for i := 0; i < 3; i++ {
		if err := n.SendSMS(context.Background(), "+998901234567", "123456"); err != nil {
			t.Fatalf("SendSMS #%d: %v", i, err)
		}
	}
	if logins, sends := f.counts(); logins != 1 || sends != 3 {
		t.Fatalf("logins=%d sends=%d, want 1 va 3", logins, sends)
	}
}

// Token eskirgan: send 401 -> avtomatik qayta login -> qayta yuborish muvaffaqiyatli, kesh yangilanadi.
func TestEskiz401Relogin(t *testing.T) {
	f := newFakeEskiz(t)
	store := &ttlStore{}
	n := New(eskizCfg(f, store), &recFallback{}, quietLog())
	if err := n.SendSMS(context.Background(), "+998901234567", "111111"); err != nil {
		t.Fatal(err)
	}
	f.expire()
	if err := n.SendSMS(context.Background(), "+998901234567", "222222"); err != nil {
		t.Fatalf("401 dan keyin qayta login ishlamadi: %v", err)
	}
	if logins, sends := f.counts(); logins != 2 || sends != 3 {
		t.Fatalf("logins=%d sends=%d, want 2 va 3", logins, sends)
	}
	if got, _ := store.Get(context.Background()); got != "tok-2" {
		t.Fatalf("keshdagi token = %q, want tok-2", got)
	}
	if f.lastForm["message"] != "NURGROUP platformasida parolni tiklash uchun tasdiqlash kodi: 222222" || f.lastAuth != "Bearer tok-2" {
		t.Fatalf("qayta yuborish: auth=%q form=%v", f.lastAuth, f.lastForm)
	}
}

// Keshda (boshqa jarayon yozgan) eskirgan token — u ham 401 orqali almashtiriladi.
func TestEskizStaleCachedToken(t *testing.T) {
	f := newFakeEskiz(t)
	store := &ttlStore{}
	_ = store.MemTokenStore.Set(context.Background(), "old-token", time.Hour)
	n := New(eskizCfg(f, store), &recFallback{}, quietLog())
	if err := n.SendSMS(context.Background(), "+998901234567", "123456"); err != nil {
		t.Fatal(err)
	}
	if logins, sends := f.counts(); logins != 1 || sends != 2 {
		t.Fatalf("logins=%d sends=%d, want 1 va 2", logins, sends)
	}
}

// Noto'g'ri email/parol: login 401 -> ErrUnauthorized, sikl yo'q, parol xatoda ko'rinmaydi.
func TestEskizWrongCredentials(t *testing.T) {
	f := newFakeEskiz(t)
	cfg := eskizCfg(f, nil)
	cfg.SMSProviderPassword = "wrong-password"
	n := New(cfg, &recFallback{}, quietLog())
	err := n.SendSMS(context.Background(), "+998901234567", "123456")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Status != http.StatusUnauthorized {
		t.Fatalf("want ProviderError{401}, got %#v", err)
	}
	if strings.Contains(err.Error(), "wrong-password") {
		t.Fatal("xato matnida parol bor")
	}
	if logins, sends := f.counts(); logins != 1 || sends != 0 {
		t.Fatalf("logins=%d sends=%d, want 1 va 0", logins, sends)
	}
}

// Qayta logindan keyin ham 401 — faqat bir marta qayta uriniladi.
func TestEskizPersistent401Bounded(t *testing.T) {
	var logins, sends int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/login" {
			logins++
			_, _ = w.Write([]byte(`{"data":{"token":"t"}}`))
			return
		}
		sends++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := &Eskiz{BaseURL: srv.URL, Email: "e", Password: "p", Tokens: &MemTokenStore{}}
	if _, err := c.Send(context.Background(), "+998901234567", "x"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
	if logins != 2 || sends != 2 {
		t.Fatalf("logins=%d sends=%d, want 2 va 2", logins, sends)
	}
}

// Login 2xx, lekin token yo'q — xato, keshga yozilmaydi, SMS yuborilmaydi.
func TestEskizEmptyTokenNotCached(t *testing.T) {
	var sends int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/login" {
			sends++
		}
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer srv.Close()
	store := &ttlStore{}
	c := &Eskiz{BaseURL: srv.URL, Email: "e", Password: "p", Tokens: store}
	if _, err := c.Send(context.Background(), "+998901234567", "x"); !errors.Is(err, ErrEmptyToken) {
		t.Fatalf("want ErrEmptyToken, got %v", err)
	}
	if store.sets != 0 || sends != 0 {
		t.Fatalf("sets=%d sends=%d, want 0 va 0", store.sets, sends)
	}
}

// Send xato statuslari: 401 dan boshqasida qayta yuborilmaydi (foydalanuvchiga ikkinchi kod bormasin).
func TestSMSStatusErrors(t *testing.T) {
	for status, want := range map[int]error{
		http.StatusPaymentRequired: ErrInsufficientBalance,
		http.StatusTooManyRequests: ErrRateLimited,
		http.StatusBadGateway:      nil,
	} {
		f := newFakeEskiz(t)
		f.sendCode = status
		n := New(eskizCfg(f, nil), &recFallback{}, quietLog())
		err := n.SendSMS(context.Background(), "+998901234567", "123456")
		var pe *ProviderError
		if !errors.As(err, &pe) || pe.Status != status || !errors.Is(pe.Err, want) && want != nil || want == nil && pe.Err != nil {
			t.Errorf("status %d: got %v", status, err)
		}
		if _, sends := f.counts(); sends != 1 {
			t.Errorf("status %d: sends = %d, want 1", status, sends)
		}
	}
}

func TestSMSInvalidPhoneNotSent(t *testing.T) {
	f := newFakeEskiz(t)
	n := New(eskizCfg(f, nil), &recFallback{}, quietLog())
	if err := n.SendSMS(context.Background(), "+79001234567", "1234"); !errors.Is(err, ErrInvalidPhone) {
		t.Fatalf("want ErrInvalidPhone, got %v", err)
	}
	if logins, sends := f.counts(); logins+sends != 0 {
		t.Fatal("noto'g'ri telefon provayderga yuborilmasligi kerak")
	}
}

// Haqiqiy Eskiz ga noto'g'ri parol bilan login (SMS yuborilmaydi, pul yechilmaydi).
// Tarmoq kerak, shuning uchun faqat SMS_LIVE_TEST=1 bo'lganda: SMS_LIVE_TEST=1 go test ./internal/notify -run Live
func TestLiveEskizWrongPassword(t *testing.T) {
	if os.Getenv("SMS_LIVE_TEST") != "1" {
		t.Skip("SMS_LIVE_TEST=1 berilmagan")
	}
	base := os.Getenv("SMS_PROVIDER_URL")
	if base == "" {
		base = EskizBaseURL
	}
	c := &Eskiz{BaseURL: base, Email: "invalid@example.com", Password: "invalid-password-for-test",
		Tokens: &MemTokenStore{}, HTTP: &http.Client{Timeout: 15 * time.Second}}
	_, err := c.Send(context.Background(), "+998901234567", "IZGO test")
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Status/100 != 4 {
		t.Fatalf("want 4xx login xatosi, got %v", err)
	}
}
