package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reviewFiles — REVIEW_* fayllarini vaqtinchalik katalogga yozadi (bo'sh qiymat — fayl yaratilmaydi).
func reviewFiles(t *testing.T, code, phones string) {
	t.Helper()
	dir := t.TempDir()
	for name, v := range map[string]string{"REVIEW_OTP_CODE": code, "REVIEW_PHONES": phones} {
		p := filepath.Join(dir, name)
		t.Setenv(name+"_FILE", p)
		if v != "" {
			if err := os.WriteFile(p, []byte(v), 0o400); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Server muhiti: production, real SMS/email, DEV_MODE=false — review baribir yoqiladi.
func TestReviewWorksInProduction(t *testing.T) {
	baseEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("SMS_MODE", "real")
	t.Setenv("EMAIL_MODE", "real")
	reviewFiles(t, "135790\n", "+998900000077, +998900000078\n")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	r := c.ReviewOTP()
	if !r.Enabled() || r.Code != "135790" || len(r.Phones) != 2 {
		t.Fatalf("review: %+v", r)
	}
	if _, ok := r.Phones["+998900000078"]; !ok {
		t.Fatal("telefon trim qilinmadi")
	}
	if c.DevOTP().Enabled() {
		t.Fatal("review dev rejimni yoqmasligi kerak")
	}
}

// Fayllar yo'q (katalog mount qilingan, fayl yaratilmagan) yoki yo'l berilmagan — o'chirilgan, xato yo'q.
func TestReviewOffWhenAbsent(t *testing.T) {
	baseEnv(t)
	if c, err := Load(); err != nil || c.ReviewOTP().Enabled() {
		t.Fatalf("yo'l yo'q: %v %+v", err, c.ReviewOTP())
	}
	reviewFiles(t, "", "")
	if c, err := Load(); err != nil || c.ReviewOTP().Enabled() {
		t.Fatalf("fayl yo'q: %v %+v", err, c.ReviewOTP())
	}
}

func TestReviewRefused(t *testing.T) {
	for name, tc := range map[string]struct{ code, phones, want string }{
		"faqat kod":       {"135790", "", "birga"},
		"faqat telefon":   {"", "+998900000077", "birga"},
		"kod qisqa":       {"13579", "+998900000077", "REVIEW_OTP_CODE"},
		"kod harf":        {"13579a", "+998900000077", "REVIEW_OTP_CODE"},
		"telefon formati": {"135790", "998900000077", "REVIEW_PHONES"},
	} {
		t.Run(name, func(t *testing.T) {
			baseEnv(t)
			reviewFiles(t, tc.code, tc.phones)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("xato kutilgan (%s): %v", tc.want, err)
			}
		})
	}
}

// Fayl bor, lekin o'qib bo'lmaydi (masalan katalog) — jim o'chib qolmasin, servis ishga tushmasin.
func TestReviewUnreadableFails(t *testing.T) {
	baseEnv(t)
	t.Setenv("REVIEW_OTP_CODE_FILE", t.TempDir())
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "REVIEW_OTP_CODE_FILE") {
		t.Fatalf("o'qilmaydigan fayl: %v", err)
	}
}
