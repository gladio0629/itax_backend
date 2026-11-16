package pii_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/izgo/shared/go/pii"
)

func TestMaskPhone(t *testing.T) {
	cases := []struct{ in, want string }{
		{"+998901234567", "998*******67"},
		{"998901234567", "998*******67"},
		{"+998 90 123 45 67", "998*******67"},
		{"+12025550123", "120******23"},
		{"12345", "***"},
		{"", "***"},
	}
	for _, c := range cases {
		if got := pii.MaskPhone(c.in); got != c.want {
			t.Errorf("MaskPhone(%q) = %q, kutilgan %q", c.in, got, c.want)
		}
	}
}

func TestMaskEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sardor@gmail.com", "s***@***.com"},
		{"admin@izgo.test", "a***@***.test"},
		{"x@mail.example.uz", "x***@***.uz"},
		{"user@localhost", "u***@***"},
		{"@gmail.com", "***"},
		{"noatsign", "***"},
		{"a@", "***"},
	}
	for _, c := range cases {
		if got := pii.MaskEmail(c.in); got != c.want {
			t.Errorf("MaskEmail(%q) = %q, kutilgan %q", c.in, got, c.want)
		}
	}
}

func TestMaskCard(t *testing.T) {
	cases := []struct{ in, want string }{
		{"8600123456781234", "**** 1234"},
		{"8600 1234 5678 1234", "**** 1234"},
		{"8600-1234-5678-9012", "**** 9012"},
		{"1234", "****"},
		{"", "****"},
	}
	for _, c := range cases {
		if got := pii.MaskCard(c.in); got != c.want {
			t.Errorf("MaskCard(%q) = %q, kutilgan %q", c.in, got, c.want)
		}
	}
}

func TestScrub(t *testing.T) {
	id := "0193a5b2-7c1e-7d3f-9a8b-998901234567" // oxirgi guruh telefonga o'xshaydi, lekin UUID
	cases := []struct{ name, in, want string }{
		{"telefon matnda", "SMS +998901234567 ga yuborildi", "SMS 998*******67 ga yuborildi"},
		{"pgx detail", `duplicate key (phone)=(+998901234567) already exists`, `duplicate key (phone)=(998*******67) already exists`},
		{"email matnda", "to=admin@izgo.test status=422", "to=a***@***.test status=422"},
		{"karta (Luhn to'g'ri)", "card 4111 1111 1111 1111 rad etildi", "card **** 1111 rad etildi"},
		{"karta ketma-ket", "pan=4111111111111111", "pan=**** 1111"},
		{"16 raqam, Luhn noto'g'ri", "ref 1234567812345678", "ref 1234567812345678"},
		{"UUID tegmaydi", id, id},
		{"UUID ichida karta shakli", "12345678-4111-1111-1111-111111111111", "12345678-4111-1111-1111-111111111111"},
		{"request_id hex", "a1b2c3d4", "a1b2c3d4"},
		{"unix ms", "1731744000000", "1731744000000"},
		{"unix ns (19 raqam)", "1731744000000000000", "1731744000000000000"},
		{"vaqt zonasi", "2026-11-16T10:00:00+05:00", "2026-11-16T10:00:00+05:00"},
		{"bo'sh", "", ""},
	}
	for _, c := range cases {
		if got := pii.Scrub(c.in); got != c.want {
			t.Errorf("%s: Scrub(%q) = %q, kutilgan %q", c.name, c.in, got, c.want)
		}
	}
}

type stringer string

func (s stringer) String() string { return string(s) }

func TestReplaceAttr(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: pii.ReplaceAttr}))
	uid := uuid.MustParse("0193a5b2-7c1e-7d3f-9a8b-998901234567")
	log.Info("kod yuborildi +998901234567",
		"phone", "+998901234567",
		"Email", "sardor@gmail.com",
		"card_number", "8600123456781234",
		"err", errors.New("provider: +998901234567 bloklangan"),
		"who", stringer("admin@izgo.test"),
		"user_id", uid,
		"driver_id", uid.String(),
		"request_id", "a1b2c3d4",
		"order_no", 998901234567,
		"amount", int64(4111111111111111),
		slog.Group("req", "phone", "+998907777777"),
	)
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"msg":         "kod yuborildi 998*******67",
		"phone":       "998*******67",
		"Email":       "s***@***.com",
		"card_number": "**** 1234",
		"err":         "provider: 998*******67 bloklangan",
		"who":         "a***@***.test",
		"user_id":     uid.String(),
		"driver_id":   uid.String(),
		"request_id":  "a1b2c3d4",
		"order_no":    float64(998901234567),
		"amount":      float64(4111111111111111),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %#v, kutilgan %#v", k, got[k], v)
		}
	}
	if g, _ := got["req"].(map[string]any); g["phone"] != "998*******77" {
		t.Errorf("guruh ichidagi telefon: %v", got["req"])
	}
	for _, leak := range []string{"+998901234567", "sardor@", "8600123456781234", "+998907777777"} {
		if strings.Contains(buf.String(), leak) {
			t.Errorf("logda ochiq PII qoldi (%q): %s", leak, buf.String())
		}
	}
}

// Helper natijasi ReplaceAttr'dan qayta o'tadi (log.Info("...", "phone", pii.MaskPhone(p))): ikki marta maskalash
// ma'lumotni buzmasligi kerak.
func TestMaskIdempotent(t *testing.T) {
	for _, c := range []struct {
		name string
		f    func(string) string
		in   string
	}{
		{"phone", pii.MaskPhone, "+998901234567"},
		{"email", pii.MaskEmail, "superadmin@izgo.test"},
		{"card", pii.MaskCard, "8600 1234 5678 9012"},
	} {
		once := c.f(c.in)
		if twice := c.f(once); twice != once {
			t.Errorf("%s: ikki marta %q -> %q", c.name, once, twice)
		}
		attr := pii.ReplaceAttr(nil, slog.String(c.name, once))
		if attr.Value.String() != once {
			t.Errorf("%s: ReplaceAttr(%q) = %q", c.name, once, attr.Value.String())
		}
	}
	if got := pii.MaskPhone(pii.MaskPhone("+998901234567")); got != "998*******67" {
		t.Errorf("telefon: %q", got)
	}
	// "*" qo'shilgan ochiq qiymat — baribir maskalanadi
	for in, want := range map[string]string{
		"+998901234567*":        "998*******67",
		"*boss@izgo.uz":         "****@***.uz",
		"8600 1234 5678 9012 *": "**** 9012",
	} {
		var got string
		switch {
		case strings.Contains(in, "@"):
			got = pii.MaskEmail(in)
		case strings.HasPrefix(in, "8600"):
			got = pii.MaskCard(in)
		default:
			got = pii.MaskPhone(in)
		}
		if got != want {
			t.Errorf("%q -> %q, kutilgan %q", in, got, want)
		}
	}
}
