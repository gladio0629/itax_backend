package authn_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/izgo/shared/go/authn"
	"github.com/redis/go-redis/v9"
)

const issuer = "izgo-identity-test"

type tok struct {
	typ, sid, role, iss string
	aud                 []string
	sub                 string
	exp                 time.Duration
}

func sign(t *testing.T, priv ed25519.PrivateKey, k tok) string {
	t.Helper()
	c := jwt.MapClaims{"iss": k.iss, "sub": k.sub, "aud": k.aud, "exp": time.Now().Add(k.exp).Unix(), "typ": k.typ, "sid": k.sid, "role": k.role}
	s, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c).SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	v := authn.NewVerifier(pub, issuer, rdb)

	uid := uuid.New()
	ok := tok{typ: "access", sid: uuid.NewString(), role: "driver", iss: issuer, aud: []string{"driver"}, sub: uid.String(), exp: time.Minute}

	id, err := v.Verify(t.Context(), sign(t, priv, ok))
	if err != nil || id.UserID != uid || id.Role != authn.RoleDriver || id.Audience != "driver" {
		t.Fatalf("haydovchi tokeni: %+v %v", id, err)
	}
	pas := ok
	pas.role, pas.aud = "passenger", []string{"client"}
	if id, err := v.Verify(t.Context(), sign(t, priv, pas)); err != nil || id.Role != authn.RolePassenger {
		t.Fatalf("yo'lovchi tokeni: %+v %v", id, err)
	}

	bad := map[string]string{}
	mut := func(name string, f func(*tok)) {
		k := ok
		f(&k)
		bad[name] = sign(t, priv, k)
	}
	mut("refresh", func(k *tok) { k.typ = "refresh" })
	mut("muddati o'tgan", func(k *tok) { k.exp = -time.Minute })
	mut("boshqa issuer", func(k *tok) { k.iss = "evil" })
	mut("admin audience", func(k *tok) { k.aud, k.role = []string{"admin"}, "superadmin" })
	mut("rol/audience mos emas", func(k *tok) { k.role = "passenger" })
	mut("ikki audience", func(k *tok) { k.aud = []string{"driver", "client"} })
	mut("sid yo'q", func(k *tok) { k.sid = "" })
	mut("sub UUID emas", func(k *tok) { k.sub = "x" })
	bad["boshqa kalit"] = sign(t, other, ok)
	bad["buzuq"] = "abc.def.ghi"
	for name, raw := range bad {
		if _, err := v.Verify(t.Context(), raw); err != authn.ErrInvalid {
			t.Errorf("%s: %v", name, err)
		}
	}

	// logout: sid blacklist'da
	mr.Set("bl:sid:"+ok.sid, "1")
	if _, err := v.Verify(t.Context(), sign(t, priv, ok)); err != authn.ErrInvalid {
		t.Fatalf("blacklist: %v", err)
	}
	// Redis ishlamasa — fail closed (ErrInvalid emas, ichki xato)
	mr.Close()
	k := ok
	k.sid = uuid.NewString()
	if _, err := v.Verify(t.Context(), sign(t, priv, k)); err == nil || err == authn.ErrInvalid {
		t.Fatalf("redis yo'q: %v", err)
	}
}
