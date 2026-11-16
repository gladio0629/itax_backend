package httpapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/izgo/identity/internal/events"
	"github.com/izgo/identity/internal/token"
	"github.com/izgo/shared/roles"
)

// schemaObj — shared/events/*.json dan kerakli qism (required, properties, enum/const).
type schemaObj struct {
	Required             []string             `json:"required"`
	AdditionalProperties *bool                `json:"additionalProperties"`
	Properties           map[string]schemaObj `json:"properties"`
	Enum                 []any                `json:"enum"`
	Const                any                  `json:"const"`
	XSubject             string               `json:"x-nats-subject"`
}

func loadSchema(t *testing.T, name string) schemaObj {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "events", name))
	if err != nil {
		t.Fatal(err)
	}
	var s schemaObj
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// checkSchema — obyekt sxemaga mos: required bor, additionalProperties=false bo'lsa ortiqcha kalit yo'q, enum/const mos.
func checkSchema(t *testing.T, where string, s schemaObj, v map[string]any) {
	t.Helper()
	for _, k := range s.Required {
		if _, ok := v[k]; !ok {
			t.Errorf("%s: majburiy %q yo'q", where, k)
		}
	}
	for k, val := range v {
		ps, ok := s.Properties[k]
		if !ok {
			if s.AdditionalProperties != nil && !*s.AdditionalProperties {
				t.Errorf("%s: sxemada yo'q kalit %q", where, k)
			}
			continue
		}
		if ps.Const != nil && ps.Const != val {
			t.Errorf("%s.%s: %v, const %v", where, k, val, ps.Const)
		}
		if len(ps.Enum) > 0 && !slices.Contains(ps.Enum, val) {
			t.Errorf("%s.%s: %v enum'da yo'q %v", where, k, val, ps.Enum)
		}
		if obj, isObj := val.(map[string]any); isObj && ps.Properties != nil {
			checkSchema(t, where+"."+k, ps, obj)
		}
	}
}

func outboxBySubject(e *env, subject string) []map[string]any {
	var out []map[string]any
	for _, ev := range e.repo.Outbox {
		if ev.Subject != subject {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(ev.Payload, &m); err != nil {
			e.t.Fatal(err)
		}
		if m["event_id"] != ev.ID.String() {
			e.t.Fatalf("event_id %v != outbox id %s", m["event_id"], ev.ID)
		}
		out = append(out, m)
	}
	return out
}

func keys(m map[string]any) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// Admin login/logout -> outbox: izgo.user.staff_logged_in.v1 / izgo.user.staff_logged_out.v1 (Backoffice audit).
func TestStaffLoginLogoutEvents(t *testing.T) {
	inSchema := loadSchema(t, "staff.logged_in.json")
	outSchema := loadSchema(t, "staff.logged_out.json")
	if inSchema.XSubject != events.SubjectStaffLoggedIn || outSchema.XSubject != events.SubjectStaffLoggedOut {
		t.Fatalf("subject sxemadagidan farq qiladi: %s %s", inSchema.XSubject, outSchema.XSubject)
	}

	e := newEnv(t)
	e.addStaff(adminEmail, adminPhone, roles.Manager, true)

	// muvaffaqiyatsiz verify — hodisa yo'q
	ch := adminStep1(e, adminEmail)
	cid := ch.str("challenge_id")
	if r := e.do("POST", "/admin/v1/auth/login/sms", map[string]string{"challenge_id": cid, "email_code": e.n.emailCode(adminEmail), "phone": adminPhone}, nil); r.Status != 202 {
		t.Fatalf("sms: %d %v", r.Status, r.Body)
	}
	if r := verify(e, cid, "000000"); r.Status != 401 {
		t.Fatalf("noto'g'ri kod: %d", r.Status)
	}
	if n := len(outboxBySubject(e, events.SubjectStaffLoggedIn)); n != 0 {
		t.Fatalf("muvaffaqiyatsiz kirishda hodisa: %d", n)
	}

	// email -> SMS -> verify: staff.logged_in, method=email
	tok := e.adminLogin(adminEmail, adminPhone)
	c, err := e.tokens.Parse(tok, token.Access, token.AudAdmin)
	if err != nil {
		t.Fatal(err)
	}
	in := outboxBySubject(e, events.SubjectStaffLoggedIn)
	if len(in) != 1 {
		t.Fatalf("logged_in: %d ta", len(in))
	}
	checkSchema(t, "logged_in", inSchema, in[0])
	p := in[0]["payload"].(map[string]any)
	if in[0]["event"] != "staff.logged_in" || p["staff_id"] != c.Subject || p["role"] != "manager" ||
		p["session_id"] != c.SID || p["method"] != "email" || p["logged_in_at"] == "" || in[0]["trace_id"] == "" {
		t.Fatalf("logged_in: %v", in[0])
	}
	for _, k := range keys(p) { // PII yo'q
		if k == "phone" || k == "email" {
			t.Fatalf("payload da PII: %v", p)
		}
	}

	// logout: staff.logged_out; takroriy (idempotent) logout dublikat bermaydi
	for i := 0; i < 2; i++ {
		if r := e.do("POST", "/admin/v1/auth/logout", nil, bearer(tok)); r.Status != 204 {
			t.Fatalf("logout %d: %d %v", i, r.Status, r.Body)
		}
	}
	out := outboxBySubject(e, events.SubjectStaffLoggedOut)
	if len(out) != 1 {
		t.Fatalf("logged_out: %d ta (1 kutilgan)", len(out))
	}
	checkSchema(t, "logged_out", outSchema, out[0])
	p = out[0]["payload"].(map[string]any)
	if out[0]["event"] != "staff.logged_out" || p["staff_id"] != c.Subject || p["role"] != "manager" ||
		p["session_id"] != c.SID || p["logged_out_at"] == "" {
		t.Fatalf("logged_out: %v", out[0])
	}
	if _, err := uuid.Parse(out[0]["event_id"].(string)); err != nil {
		t.Fatal(err)
	}

	// yaroqsiz token bilan logout — 401, hodisa yo'q
	if r := e.do("POST", "/admin/v1/auth/logout", nil, bearer("x.y.z")); r.Status != 401 {
		t.Fatalf("yaroqsiz token: %d", r.Status)
	}
	if n := len(outboxBySubject(e, events.SubjectStaffLoggedOut)); n != 1 {
		t.Fatalf("yaroqsiz token hodisa qo'shdi: %d", n)
	}

	// email'siz operator: telefon -> SMS -> verify, method=sms
	e.addStaff("", operatorPhone, roles.Operator, true)
	r := phoneStep(e, operatorPhone)
	if r.Status != 202 {
		t.Fatalf("telefon: %d %v", r.Status, r.Body)
	}
	if r = verify(e, r.str("challenge_id"), e.n.smsCode(operatorPhone)); r.Status != 200 {
		t.Fatalf("operator verify: %d %v", r.Status, r.Body)
	}
	in = outboxBySubject(e, events.SubjectStaffLoggedIn)
	if len(in) != 2 {
		t.Fatalf("logged_in: %d ta", len(in))
	}
	if p = in[1]["payload"].(map[string]any); p["method"] != "sms" || p["role"] != "operator" {
		t.Fatalf("operator: %v", in[1])
	}
	checkSchema(t, "logged_in(operator)", inSchema, in[1])
}
