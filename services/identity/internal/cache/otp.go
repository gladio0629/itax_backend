// Package cache — Redis: OTP kodlari, tezlik cheklovi (rate limit) va token blacklist.
package cache

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"
)

// OTPResult — kodni tekshirish natijasi.
type OTPResult int

const (
	OTPOK      OTPResult = iota // to'g'ri; kod o'chirildi (bir martalik)
	OTPInvalid                  // noto'g'ri; AttemptsLeft qoldi
	OTPExpired                  // kod yo'q: muddati tugagan, ishlatilgan yoki urinishlar tugagan
)

// OTP — kodlarni Redis'da saqlaydi. Kod ochiq holda emas, sha256 hash ko'rinishida turadi.
type OTP struct{ rdb redis.Cmdable }

func NewOTP(rdb redis.Cmdable) *OTP { return &OTP{rdb: rdb} }

// NewCode — kriptografik tasodifiy, `digits` xonali kod (bosh nollar bilan).
func NewCode(digits int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("otp: tasodifiy kod: %w", err)
	}
	return fmt.Sprintf("%0*d", digits, n), nil
}

func codeHash(key, code string) string {
	sum := sha256.Sum256([]byte(key + "\x00" + code))
	return hex.EncodeToString(sum[:])
}

// Put — kodni saqlaydi (oldingisini va urinishlar hisobini almashtiradi).
func (o *OTP) Put(ctx context.Context, key, code string, ttl time.Duration) error {
	pipe := o.rdb.TxPipeline()
	pipe.HSet(ctx, key, "h", codeHash(key, code), "n", 0)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("otp: saqlash: %w", err)
	}
	return nil
}

// Lua: tekshirish + noto'g'ri urinishlar hisobi bitta atomik amal.
// Natija: {1,0} to'g'ri | {0,qolgan} noto'g'ri | {-1,0} kod yo'q.
var checkScript = redis.NewScript(`
local h = redis.call('HGET', KEYS[1], 'h')
if not h then return {-1, 0} end
if h == ARGV[1] then
  redis.call('DEL', KEYS[1])
  return {1, 0}
end
local n = redis.call('HINCRBY', KEYS[1], 'n', 1)
local max = tonumber(ARGV[2])
if n >= max then
  redis.call('DEL', KEYS[1])
  return {0, 0}
end
return {0, max - n}
`)

// Check — kodni tekshiradi. maxAttempts-chi noto'g'ri urinishdan keyin kod o'chadi
// (keyingi urinish OTPExpired beradi).
func (o *OTP) Check(ctx context.Context, key, code string, maxAttempts int) (OTPResult, int, error) {
	res, err := checkScript.Run(ctx, o.rdb, []string{key}, codeHash(key, code), maxAttempts).Slice()
	if err != nil {
		return OTPExpired, 0, fmt.Errorf("otp: tekshirish: %w", err)
	}
	if len(res) != 2 {
		return OTPExpired, 0, fmt.Errorf("otp: kutilmagan javob: %v", res)
	}
	status, _ := res[0].(int64)
	left, _ := res[1].(int64)
	switch status {
	case 1:
		return OTPOK, 0, nil
	case 0:
		return OTPInvalid, int(left), nil
	default:
		return OTPExpired, 0, nil
	}
}

// Delete — kodni o'chiradi.
func (o *OTP) Delete(ctx context.Context, key string) error {
	if err := o.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("otp: o'chirish: %w", err)
	}
	return nil
}

var hitScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {n, ttl}
`)

// Hit — hisoblagichni 1 ga oshiradi (oyna birinchi urinishdan boshlanadi).
// limit oshib ketgan bo'lsa ok=false va qancha kutish kerakligi qaytadi.
func (o *OTP) Hit(ctx context.Context, key string, limit int, window time.Duration) (ok bool, retryAfter time.Duration, err error) {
	secs := int(window.Seconds())
	if secs < 1 {
		secs = 1
	}
	res, err := hitScript.Run(ctx, o.rdb, []string{key}, secs).Slice()
	if err != nil {
		return false, 0, fmt.Errorf("ratelimit: %w", err)
	}
	if len(res) != 2 {
		return false, 0, fmt.Errorf("ratelimit: kutilmagan javob: %v", res)
	}
	n, _ := res[0].(int64)
	ttl, _ := res[1].(int64)
	if n > int64(limit) {
		return false, time.Duration(ttl) * time.Second, nil
	}
	return true, 0, nil
}

// Cooldown — kalit hali amal qilayotgan bo'lsa, qolgan vaqtni qaytaradi (aks holda 0).
func (o *OTP) Cooldown(ctx context.Context, key string) (time.Duration, error) {
	d, err := o.rdb.PTTL(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("cooldown: %w", err)
	}
	if d <= 0 { // -1 / -2 — kalit yo'q yoki muddatsiz
		return 0, nil
	}
	return d, nil
}

// StartCooldown — qayta so'rash taqiqlangan oynani boshlaydi.
func (o *OTP) StartCooldown(ctx context.Context, key string, d time.Duration) error {
	if err := o.rdb.Set(ctx, key, "1", d).Err(); err != nil {
		return fmt.Errorf("cooldown: %w", err)
	}
	return nil
}

// Blacklist — bekor qilingan sessiyalar (sid). Access token'lar 15 daqiqa yashagani uchun
// yozuvlar access TTL gacha saqlanadi; refresh tokenlar esa bazada bekor qilinadi.
type Blacklist struct{ rdb redis.Cmdable }

func NewBlacklist(rdb redis.Cmdable) *Blacklist { return &Blacklist{rdb: rdb} }

func blKey(sid string) string { return "bl:sid:" + sid }

func (b *Blacklist) Revoke(ctx context.Context, ttl time.Duration, sids ...string) error {
	if len(sids) == 0 {
		return nil
	}
	pipe := b.rdb.TxPipeline()
	for _, s := range sids {
		pipe.Set(ctx, blKey(s), "1", ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("blacklist: %w", err)
	}
	return nil
}

func (b *Blacklist) IsRevoked(ctx context.Context, sid string) (bool, error) {
	n, err := b.rdb.Exists(ctx, blKey(sid)).Result()
	if err != nil {
		return false, fmt.Errorf("blacklist: %w", err)
	}
	return n > 0, nil
}

// SetValue / GetValue — qisqa muddatli oddiy qiymatlar (masalan, admin challenge metama'lumoti).
func (o *OTP) SetValue(ctx context.Context, key, val string, ttl time.Duration) error {
	if err := o.rdb.Set(ctx, key, val, ttl).Err(); err != nil {
		return fmt.Errorf("cache: set: %w", err)
	}
	return nil
}

func (o *OTP) GetValue(ctx context.Context, key string) (string, bool, error) {
	v, err := o.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("cache: get: %w", err)
	}
	return v, true, nil
}
