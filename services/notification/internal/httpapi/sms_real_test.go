package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/izgo/notification/internal/sms"
)

const providerKey = "test-good-key"

type providerCalls struct {
	mu     sync.Mutex
	n      int
	apiKey string
	body   map[string]any
}

// fakeXabarnoma — smsxabarnoma.uz o'rnida: X-API-Key noto'g'ri bo'lsa 401.
func fakeXabarnoma(t *testing.T, pc *providerCalls) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pc.mu.Lock()
		defer pc.mu.Unlock()
		pc.n++
		pc.apiKey = r.Header.Get("X-API-Key")
		data, _ := io.ReadAll(r.Body)
		pc.body = map[string]any{}
		_ = json.Unmarshal(data, &pc.body)
		if r.URL.Path != "/api/v1/sms/send" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if pc.apiKey != providerKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":"msg-1","status":"QUEUED","parts":1,"cost":"60"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSMSRealModeSends(t *testing.T) {
	var pc providerCalls
	prov := fakeXabarnoma(t, &pc)
	e := newEnv(t, func(o *opts) { o.smsSender = sms.Xabarnoma{BaseURL: prov.URL + "/api/v1", APIKey: providerKey} })
	body := map[string]any{"phone": "+998901234567", "template_key": "otp", "params": map[string]string{"code": "482913"}}
	if r := e.do("POST", "/internal/notification/sms", internal(), body); r.Status != 202 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if pc.n != 1 || pc.body["phone"] != "998901234567" || !strings.Contains(pc.body["text"].(string), "482913") {
		t.Fatalf("provayder so'rovi: n=%d body=%v", pc.n, pc.body)
	}
	if ext, _ := pc.body["externalId"].(string); ext == "" || ext != pc.body["idempotencyKey"] {
		t.Errorf("externalId/idempotencyKey: %v / %v", pc.body["externalId"], pc.body["idempotencyKey"])
	}
}

// SMS_MODE=real + noto'g'ri kalit: provayder 401 -> endpoint 500 INTERNAL_ERROR (tafsilot oshkor etilmaydi).
func TestSMSRealModeWrongKey(t *testing.T) {
	var pc providerCalls
	prov := fakeXabarnoma(t, &pc)
	e := newEnv(t, func(o *opts) { o.smsSender = sms.Xabarnoma{BaseURL: prov.URL + "/api/v1", APIKey: "wrong-key"} })
	body := map[string]any{"phone": "+998901234567", "template_key": "otp", "params": map[string]string{"code": "482913"}}
	r := e.do("POST", "/internal/notification/sms", internal(), body)
	if r.Status != 500 || r.errKey() != "INTERNAL_ERROR" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if pc.n != 1 || pc.apiKey != "wrong-key" {
		t.Fatalf("provayder chaqirilmadi: n=%d", pc.n)
	}
	if strings.Contains(string(r.Raw), "401") || strings.Contains(string(r.Raw), "wrong-key") {
		t.Errorf("javobda provayder tafsiloti: %s", r.Raw)
	}
}
