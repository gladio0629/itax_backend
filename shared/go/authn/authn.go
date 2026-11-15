// Package authn — Identity chiqargan access tokenlarni tekshiradi (faqat public kalit bilan).
// Go servislar (trip-order, matching-geo) uchun umumiy.
//
// Tekshiruv: EdDSA imzo, issuer, muddat, typ=access, yagona audience (client|driver), rol audience'ga mosligi,
// va Redis'dagi `bl:sid:<sid>` blacklist (Identity logout / refresh qayta ishlatilganda yozadi).
package authn

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrInvalid = errors.New("authn: invalid token")

const (
	RolePassenger = "passenger"
	RoleDriver    = "driver"
)

type claims struct {
	jwt.RegisteredClaims
	Type  string   `json:"typ"`
	SID   string   `json:"sid"`
	Role  string   `json:"role"`
	Perms []string `json:"perms"`
}

// Identity — tekshirilgan token egasi.
type Identity struct {
	UserID   uuid.UUID
	Role     string // passenger | driver
	Audience string // client | driver (X-App qiymatiga mos kelishi kerak)
	Perms    []string
}

type Verifier struct {
	pub    ed25519.PublicKey
	issuer string
	rdb    redis.Cmdable
	now    func() time.Time
}

func NewVerifier(pub ed25519.PublicKey, issuer string, rdb redis.Cmdable) *Verifier {
	return &Verifier{pub: pub, issuer: issuer, rdb: rdb, now: time.Now}
}

func LoadPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("authn: public kalit: %w", err)
	}
	b, _ := pem.Decode(raw)
	if b == nil {
		return nil, errors.New("authn: public kalit PEM emas")
	}
	k, err := x509.ParsePKIXPublicKey(b.Bytes)
	if err != nil {
		return nil, fmt.Errorf("authn: public kalit (PKIX): %w", err)
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("authn: public kalit Ed25519 emas")
	}
	return pub, nil
}

// Verify — noto'g'ri/muddati o'tgan/bekor qilingan token uchun ErrInvalid; Redis xatosi alohida qaytadi (fail-closed).
func (v *Verifier) Verify(ctx context.Context, raw string) (*Identity, error) {
	c := &claims{}
	p := jwt.NewParser(jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(v.issuer), jwt.WithExpirationRequired(), jwt.WithTimeFunc(v.now))
	if _, err := p.ParseWithClaims(raw, c, func(*jwt.Token) (any, error) { return v.pub, nil }); err != nil {
		return nil, ErrInvalid
	}
	if c.Type != "access" || c.SID == "" || len(c.Audience) != 1 {
		return nil, ErrInvalid
	}
	id, err := uuid.Parse(c.Subject)
	if err != nil {
		return nil, ErrInvalid
	}
	aud := c.Audience[0]
	switch {
	case aud == "client" && c.Role == RolePassenger:
	case aud == "driver" && c.Role == RoleDriver:
	default:
		return nil, ErrInvalid // admin tokeni yoki audience/rol mos emas
	}
	n, err := v.rdb.Exists(ctx, "bl:sid:"+c.SID).Result()
	if err != nil {
		return nil, fmt.Errorf("authn: blacklist: %w", err)
	}
	if n > 0 {
		return nil, ErrInvalid
	}
	return &Identity{UserID: id, Role: c.Role, Audience: aud, Perms: c.Perms}, nil
}
