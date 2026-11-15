// Package token — Ed25519 (EdDSA) bilan imzolangan JWT: access va refresh.
package token

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Audience string

// Har ilova uchun alohida audience.
const (
	AudClient Audience = "client"
	AudDriver Audience = "driver"
	AudAdmin  Audience = "admin"
)

type Type string

const (
	Access  Type = "access"
	Refresh Type = "refresh"
)

var (
	ErrInvalid = errors.New("token: invalid")
	ErrExpired = errors.New("token: expired")
)

// Claims — token ichidagi ma'lumot. sid — sessiya (refresh oilasi) ID si:
// bekor qilish shu bo'yicha ishlaydi. Access token'da role va permissions bor.
type Claims struct {
	jwt.RegisteredClaims
	Type  Type     `json:"typ"`
	SID   string   `json:"sid"`
	Role  string   `json:"role,omitempty"`
	Perms []string `json:"perms,omitempty"`
}

// Audience — token yagona audience bilan beriladi.
func (c *Claims) Aud() Audience {
	if len(c.Audience) == 0 {
		return ""
	}
	return Audience(c.Audience[0])
}

type Manager struct {
	priv   ed25519.PrivateKey
	pub    ed25519.PublicKey
	issuer string
	kid    string
	now    func() time.Time
}

func NewManager(priv ed25519.PrivateKey, pub ed25519.PublicKey, issuer string) (*Manager, error) {
	if len(priv) != ed25519.PrivateKeySize || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("token: kalit uzunligi noto'g'ri")
	}
	if !priv.Public().(ed25519.PublicKey).Equal(pub) {
		return nil, errors.New("token: public kalit private kalitga mos kelmaydi")
	}
	sum := sha256.Sum256(pub)
	return &Manager{
		priv: priv, pub: pub, issuer: issuer,
		kid: base64.RawURLEncoding.EncodeToString(sum[:8]),
		now: time.Now,
	}, nil
}

// WithClock — testlar uchun soatni almashtiradi.
func (m *Manager) WithClock(now func() time.Time) *Manager {
	cp := *m
	cp.now = now
	return &cp
}

// LoadKeys — PEM fayllardan Ed25519 kalitlarini o'qiydi.
func LoadKeys(privPath, pubPath string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	privPEM, err := os.ReadFile(privPath)
	if err != nil {
		return nil, nil, fmt.Errorf("token: private kalit: %w", err)
	}
	pubPEM, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, nil, fmt.Errorf("token: public kalit: %w", err)
	}
	pb, _ := pem.Decode(privPEM)
	if pb == nil {
		return nil, nil, errors.New("token: private kalit PEM emas")
	}
	pk, err := x509.ParsePKCS8PrivateKey(pb.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("token: private kalit (PKCS#8): %w", err)
	}
	priv, ok := pk.(ed25519.PrivateKey)
	if !ok {
		return nil, nil, errors.New("token: private kalit Ed25519 emas")
	}
	ub, _ := pem.Decode(pubPEM)
	if ub == nil {
		return nil, nil, errors.New("token: public kalit PEM emas")
	}
	pu, err := x509.ParsePKIXPublicKey(ub.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("token: public kalit (PKIX): %w", err)
	}
	pub, ok := pu.(ed25519.PublicKey)
	if !ok {
		return nil, nil, errors.New("token: public kalit Ed25519 emas")
	}
	return priv, pub, nil
}

type IssueParams struct {
	Type      Type
	Audience  Audience
	Subject   string
	SessionID string
	JTI       string
	Role      string
	Perms     []string
	TTL       time.Duration
}

// Issue — token imzolaydi.
func (m *Manager) Issue(p IssueParams) (string, *Claims, error) {
	if p.Subject == "" || p.SessionID == "" || p.JTI == "" || p.TTL <= 0 || p.Audience == "" {
		return "", nil, errors.New("token: to'liq bo'lmagan parametrlar")
	}
	now := m.now()
	c := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   p.Subject,
			Audience:  jwt.ClaimStrings{string(p.Audience)},
			ID:        p.JTI,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(p.TTL)),
		},
		Type: p.Type, SID: p.SessionID, Role: p.Role, Perms: p.Perms,
	}
	t := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	t.Header["kid"] = m.kid
	s, err := t.SignedString(m.priv)
	if err != nil {
		return "", nil, fmt.Errorf("token: imzolash: %w", err)
	}
	return s, c, nil
}

// Parse — imzo, muddat, issuer, tur (access/refresh) va audience'ni tekshiradi.
// Muddati tugagan bo'lsa ErrExpired, qolgan barcha holatda ErrInvalid.
func (m *Manager) Parse(raw string, typ Type, auds ...Audience) (*Claims, error) {
	c := &Claims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(m.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(m.now),
	)
	_, err := parser.ParseWithClaims(raw, c, func(*jwt.Token) (any, error) { return m.pub, nil })
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpired
		}
		return nil, ErrInvalid
	}
	if c.Type != typ || c.Subject == "" || c.SID == "" || c.ID == "" || len(c.Audience) != 1 {
		return nil, ErrInvalid
	}
	for _, a := range auds {
		if c.Aud() == a {
			return c, nil
		}
	}
	return nil, ErrInvalid
}
