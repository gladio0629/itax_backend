package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newMgr(t *testing.T) (*Manager, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(priv, pub, "iss")
	if err != nil {
		t.Fatal(err)
	}
	return m, priv
}

func params(typ Type, aud Audience) IssueParams {
	return IssueParams{Type: typ, Audience: aud, Subject: "u1", SessionID: "s1", JTI: "j1", Role: "passenger", Perms: []string{"a:b"}, TTL: time.Minute}
}

func TestParse(t *testing.T) {
	m, priv := newMgr(t)
	now := time.Now()

	good, _, err := m.Issue(params(Access, AudClient))
	if err != nil {
		t.Fatal(err)
	}
	other, _ := newMgr(t)
	foreign, _, _ := other.Issue(params(Access, AudClient))

	// "none" algoritmi va HS256 bilan soxtalash
	none := jwt.NewWithClaims(jwt.SigningMethodNone, &Claims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: "iss", Subject: "u1", ID: "j1", Audience: jwt.ClaimStrings{"client"}, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))},
		Type: Access, SID: "s1"})
	noneStr, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, &Claims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: "iss", Subject: "u1", ID: "j1", Audience: jwt.ClaimStrings{"client"}, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))},
		Type: Access, SID: "s1"})
	hsStr, _ := hs.SignedString(priv.Public().(ed25519.PublicKey))

	wrongIss := newMgrWithIssuer(t, priv, "evil")
	badIss, _, _ := wrongIss.Issue(params(Access, AudClient))

	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + parts[1] + "x." + parts[2]

	refresh, _, _ := m.Issue(params(Refresh, AudClient))
	adminTok, _, _ := m.Issue(params(Access, AudAdmin))

	cases := []struct {
		name string
		raw  string
		typ  Type
		auds []Audience
		want error
	}{
		{"yaroqli", good, Access, []Audience{AudClient}, nil},
		{"bir nechta audience dan biri", good, Access, []Audience{AudDriver, AudClient}, nil},
		{"boshqa audience", good, Access, []Audience{AudDriver}, ErrInvalid},
		{"admin tokeni user audience'da", adminTok, Access, []Audience{AudClient, AudDriver}, ErrInvalid},
		{"refresh access sifatida", refresh, Access, []Audience{AudClient}, ErrInvalid},
		{"access refresh sifatida", good, Refresh, []Audience{AudClient}, ErrInvalid},
		{"begona kalit", foreign, Access, []Audience{AudClient}, ErrInvalid},
		{"alg=none", noneStr, Access, []Audience{AudClient}, ErrInvalid},
		{"alg=HS256 (public kalit sir sifatida)", hsStr, Access, []Audience{AudClient}, ErrInvalid},
		{"begona issuer", badIss, Access, []Audience{AudClient}, ErrInvalid},
		{"o'zgartirilgan", tampered, Access, []Audience{AudClient}, ErrInvalid},
		{"axlat", "xyz", Access, []Audience{AudClient}, ErrInvalid},
	}
	for _, c := range cases {
		_, err := m.Parse(c.raw, c.typ, c.auds...)
		if !errors.Is(err, c.want) && !(c.want == nil && err == nil) {
			t.Errorf("%s: err = %v, kutilgan %v", c.name, err, c.want)
		}
	}
}

func newMgrWithIssuer(t *testing.T, priv ed25519.PrivateKey, iss string) *Manager {
	t.Helper()
	m, err := NewManager(priv, priv.Public().(ed25519.PublicKey), iss)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestExpiry(t *testing.T) {
	m, _ := newMgr(t)
	tok, c, _ := m.Issue(params(Access, AudClient))

	later := m.WithClock(func() time.Time { return c.ExpiresAt.Add(time.Second) })
	if _, err := later.Parse(tok, Access, AudClient); !errors.Is(err, ErrExpired) {
		t.Fatalf("muddati o'tgan token: %v", err)
	}
	before := m.WithClock(func() time.Time { return c.ExpiresAt.Add(-time.Second) })
	if _, err := before.Parse(tok, Access, AudClient); err != nil {
		t.Fatalf("hali yaroqli: %v", err)
	}
}

func TestIssueRejectsIncomplete(t *testing.T) {
	m, _ := newMgr(t)
	p := params(Access, AudClient)
	p.TTL = 0
	if _, _, err := m.Issue(p); err == nil {
		t.Fatal("TTL=0 qabul qilindi")
	}
}

func TestNewManagerKeyMismatch(t *testing.T) {
	_, priv := newMgr(t)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := NewManager(priv, otherPub, "iss"); err == nil {
		t.Fatal("mos kelmaydigan kalitlar qabul qilindi")
	}
}

func TestLoadKeys(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	privDER, _ := x509.MarshalPKCS8PrivateKey(priv)
	pubDER, _ := x509.MarshalPKIXPublicKey(pub)
	pp, up := filepath.Join(dir, "private.pem"), filepath.Join(dir, "public.pem")
	_ = os.WriteFile(pp, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600)
	_ = os.WriteFile(up, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o644)

	gp, gu, err := LoadKeys(pp, up)
	if err != nil || !gp.Equal(priv) || !gu.Equal(pub) {
		t.Fatalf("LoadKeys: %v", err)
	}
	if _, _, err := LoadKeys(filepath.Join(dir, "yo'q.pem"), up); err == nil {
		t.Fatal("mavjud bo'lmagan fayl xato bermadi")
	}
	bad := filepath.Join(dir, "bad.pem")
	_ = os.WriteFile(bad, []byte("not pem"), 0o600)
	if _, _, err := LoadKeys(bad, up); err == nil {
		t.Fatal("buzuq PEM xato bermadi")
	}
}
