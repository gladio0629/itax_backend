// Package pii — loglarda shaxsiy ma'lumotni (telefon, email, karta) maskalash.
//
// Ikki qatlam:
//   - helperlar: MaskPhone / MaskEmail / MaskCard — log yozishdan oldin aniq maydon uchun;
//   - ReplaceAttr: slog.HandlerOptions.ReplaceAttr uchun — kalit nomi bo'yicha (phone, email, card, ...) to'liq maskalaydi
//     va har qanday satr qiymati (xabar, err ichidagi pgx/provayder matni ham) ichidan +E.164 telefon, email va
//     Luhn-to'g'ri karta raqamini qidirib almashtiradi. UUID, request_id va sonli qiymatlar o'zgarmaydi.
//
// Formatlar: telefon "998*******67", email "s***@***.com", karta "**** 1234".
package pii

import (
	"log/slog"
	"regexp"
	"strings"
)

// MaskPhone — "+998901234567" -> "998*******67": birinchi 3 va oxirgi 2 raqam qoladi, "+" va boshqa belgilar tashlanadi.
// Helper natijalarining aniq shakli: ReplaceAttr'dan qayta o'tganda o'zgarmasin (aks holda "998*******67" -> "***",
// "**** 1234" -> "****"). Faqat to'liq mos shakl o'tkaziladi — "*" qo'shilgan ochiq raqam ("+998901234567*") emas.
var (
	maskedPhoneRe = regexp.MustCompile(`^[0-9]{3}\*+[0-9]{2}$|^\*\*\*$`)
	maskedEmailRe = regexp.MustCompile(`^[^@\s]\*\*\*@\*\*\*(\.[^@\s.]+)?$|^\*\*\*$`)
	maskedCardRe  = regexp.MustCompile(`^\*\*\*\* [0-9]{4}$|^\*\*\*\*$`)
)

func MaskPhone(p string) string {
	if maskedPhoneRe.MatchString(p) {
		return p
	}
	d := digits(p)
	if len(d) < 7 {
		return "***"
	}
	return d[:3] + strings.Repeat("*", len(d)-5) + d[len(d)-2:]
}

// MaskEmail — "sardor@gmail.com" -> "s***@***.com": lokal qismning birinchi belgisi va domenning oxirgi zonasi qoladi.
func MaskEmail(e string) string {
	if maskedEmailRe.MatchString(e) {
		return e
	}
	e = strings.TrimSpace(e)
	at := strings.LastIndexByte(e, '@')
	if at < 1 || at == len(e)-1 {
		return "***"
	}
	dom := e[at+1:]
	dot := strings.LastIndexByte(dom, '.')
	if dot < 0 || dot == len(dom)-1 {
		return e[:1] + "***@***"
	}
	return e[:1] + "***@***" + dom[dot:]
}

// MaskCard — "8600 1234 5678 1234" -> "**** 1234": faqat oxirgi 4 raqam.
func MaskCard(c string) string {
	if maskedCardRe.MatchString(c) {
		return c
	}
	d := digits(c)
	if len(d) < 8 {
		return "****"
	}
	return "**** " + d[len(d)-4:]
}

func digits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Kalit nomi bo'yicha (kichik harfda; guruh prefiksi hisobga olinmaydi) maskalanadigan maydonlar.
var (
	phoneKeys = set("phone", "phone_number", "msisdn", "recipient_phone", "receiver_phone", "sender_phone", "staff_phone")
	emailKeys = set("email", "email_address", "staff_email")
	cardKeys  = set("card", "card_number", "card_pan", "pan", "masked_pan")
)

func set(keys ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}

// ReplaceAttr — slog.HandlerOptions.ReplaceAttr:
//
//	slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{ReplaceAttr: pii.ReplaceAttr}))
func ReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		s := a.Value.String()
		key := strings.ToLower(a.Key)
		switch {
		case has(phoneKeys, key):
			return slog.String(a.Key, MaskPhone(s))
		case has(emailKeys, key):
			return slog.String(a.Key, MaskEmail(s))
		case has(cardKeys, key):
			return slog.String(a.Key, MaskCard(s))
		}
		if r := Scrub(s); r != s {
			return slog.String(a.Key, r)
		}
	case slog.KindAny:
		// error va fmt.Stringer — JSON'ga matn bo'lib chiqadi; ichida PII bo'lishi mumkin (pgx detail, provayder javobi).
		var s string
		switch v := a.Value.Any().(type) {
		case error:
			s = v.Error()
		case interface{ String() string }:
			s = v.String()
		default:
			return a
		}
		if r := Scrub(s); r != s {
			return slog.String(a.Key, r)
		}
	}
	return a
}

func has(m map[string]struct{}, k string) bool { _, ok := m[k]; return ok }

var (
	// +E.164: "+" belgisi majburiy — UUID/hex/timestamp ichidagi raqam ketma-ketliklari tegmasin.
	phoneRe = regexp.MustCompile(`\+[1-9][0-9]{9,14}`)
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	// 16 raqam, 4 talik guruhlar orasida ixtiyoriy bo'sh joy yoki "-". Chegaralar va Luhn alohida tekshiriladi.
	cardRe = regexp.MustCompile(`[0-9]{4}[ \-]?[0-9]{4}[ \-]?[0-9]{4}[ \-]?[0-9]{4}`)
)

// Scrub — matn ichidagi telefon, email va karta raqamlarini maskalaydi.
func Scrub(s string) string {
	if !strings.ContainsAny(s, "+@0123456789") {
		return s
	}
	if strings.IndexByte(s, '@') >= 0 {
		s = emailRe.ReplaceAllStringFunc(s, MaskEmail)
	}
	if strings.IndexByte(s, '+') >= 0 {
		s = phoneRe.ReplaceAllStringFunc(s, MaskPhone)
	}
	return scrubCards(s)
}

func scrubCards(s string) string {
	locs := cardRe.FindAllStringIndex(s, -1)
	if locs == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, l := range locs {
		i, j := l[0], l[1]
		// chap/o'ng qo'shni harf, raqam yoki "-" bo'lsa — bu UUID, hex yoki uzunroq son bo'lagi, karta emas
		if (i > 0 && idChar(s[i-1])) || (j < len(s) && idChar(s[j])) || !luhn(digits(s[i:j])) {
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString(MaskCard(s[i:j]))
		last = j
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func idChar(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_'
}

func luhn(d string) bool {
	sum := 0
	double := false
	for i := len(d) - 1; i >= 0; i-- {
		n := int(d[i] - '0')
		if double {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		double = !double
	}
	return len(d) > 0 && sum%10 == 0
}
