// Package store — Redis: bildirishnomalar (TTL 30 kun), qurilmalar, buyurtma->yo'lovchi xaritasi, WebSocket ulanishlar limiti.
//
//	notif:{user}               ZSET  a'zo = bildirishnoma ID (UUIDv7), ball = yaratilgan vaqt (ms)
//	notif:{user}:{id}          STRING JSON, EX NotificationTTL
//	user:{id}:devices          HASH  token -> platform (fcm | apns)
//	device:{sha256(token)}     STRING egasi user ID (token boshqa foydalanuvchiga o'tsa eski egasidan olinadi)
//	order:{id}:passenger       STRING yo'lovchi ID (order.accepted'da yo'lovchini topish uchun)
//	ws:conns:{user}            ZSET  ulanish ID -> amal qilish muddati (F-19 limit)
//	ws:user:{id}               PUB/SUB kanali (real-time xabarlar)
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Options struct {
	TTL        time.Duration
	MaxPerUser int
	MaxDevices int
}

type Store struct {
	rdb redis.Cmdable
	opt Options
}

func New(rdb redis.Cmdable, opt Options) *Store { return &Store{rdb: rdb, opt: opt} }

// ---------------------------------------------------------------- bildirishnomalar

// Notification — openapi `Notification`.
type Notification struct {
	ID        uuid.UUID         `json:"id"`
	Kind      string            `json:"kind"`
	TitleKey  string            `json:"title_key"`
	BodyKey   string            `json:"body_key,omitempty"`
	Params    map[string]string `json:"params"`
	Read      bool              `json:"read"`
	CreatedAt time.Time         `json:"created_at"`
}

func zKey(u uuid.UUID) string       { return "notif:" + u.String() }
func nKey(u, id uuid.UUID) string   { return "notif:" + u.String() + ":" + id.String() }
func WSChannel(u uuid.UUID) string  { return "ws:user:" + u.String() }
func orderKey(o uuid.UUID) string   { return "order:" + o.String() + ":passenger" }
func devicesKey(u uuid.UUID) string { return "user:" + u.String() + ":devices" }
func connsKey(u uuid.UUID) string   { return "ws:conns:" + u.String() }
func deviceOwnerKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return "device:" + hex.EncodeToString(h[:])
}

// Bitta atomik amal: yozuv (NX — takroriy yetkazishda ikkinchi marta yaratilmaydi) + ro'yxat + eskirganlarni/ortiqchalarni tozalash.
var addScript = redis.NewScript(`
if not redis.call('SET', KEYS[2], ARGV[1], 'NX', 'EX', ARGV[2]) then return 0 end
redis.call('ZADD', KEYS[1], ARGV[3], ARGV[4])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[5])
redis.call('ZREMRANGEBYRANK', KEYS[1], 0, -(tonumber(ARGV[6]) + 1))
redis.call('EXPIRE', KEYS[1], ARGV[2])
return 1
`)

// NotificationID — (hodisa, foydalanuvchi) bo'yicha deterministik: takroriy yetkazish bir xil ID beradi.
var idNS = uuid.MustParse("3b7a2f0e-6c41-4d58-9a1e-8e2d4c5b7f90")

func NotificationID(event, user uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(idNS, []byte(event.String()+":"+user.String()))
}

// Add — bildirishnomani saqlaydi. created=false: bu hodisa uchun allaqachon bor (takroriy yetkazish).
func (s *Store) Add(ctx context.Context, user uuid.UUID, n Notification) (bool, error) {
	body, err := json.Marshal(n)
	if err != nil {
		return false, err
	}
	ttl := int(s.opt.TTL.Seconds())
	cutoff := n.CreatedAt.Add(-s.opt.TTL).UnixMilli()
	res, err := addScript.Run(ctx, s.rdb, []string{zKey(user), nKey(user, n.ID)},
		string(body), ttl, n.CreatedAt.UnixMilli(), n.ID.String(), cutoff, s.opt.MaxPerUser).Int()
	if err != nil {
		return false, fmt.Errorf("store: add: %w", err)
	}
	return res == 1, nil
}

// Cursor: "<ms>:<id>" (base64 emas, oddiy; klientga noshaffof).
func EncodeCursor(n Notification) string {
	return strconv.FormatInt(n.CreatedAt.UnixMilli(), 10) + "." + n.ID.String()
}

var ErrBadCursor = errors.New("store: bad cursor")

func decodeCursor(c string) (int64, string, error) {
	ms, id, ok := strings.Cut(c, ".")
	v, err := strconv.ParseInt(ms, 10, 64)
	if !ok || err != nil {
		return 0, "", ErrBadCursor
	}
	if _, err := uuid.Parse(id); err != nil {
		return 0, "", ErrBadCursor
	}
	return v, id, nil
}

// List — foydalanuvchining bildirishnomalari, yangidan eskiga. limit+1 tagacha qaytaradi (oxirgisi "keyingi sahifa bor" belgisi).
func (s *Store) List(ctx context.Context, user uuid.UUID, cursor string, limit int) ([]Notification, error) {
	max := "+inf"
	curID := ""
	if cursor != "" {
		ms, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		max, curID = strconv.FormatInt(ms, 10), id
	}
	// Bir xil millisekundli yozuvlar uchun zaxira: kursor bo'yicha allaqachon berilganlar o'tkazib yuboriladi.
	const slack = 100
	var out []Notification
	var dead []any
	offset := int64(0)
	for len(out) < limit+1 {
		members, err := s.rdb.ZRevRangeByScoreWithScores(ctx, zKey(user), &redis.ZRangeBy{Max: max, Min: "-inf", Offset: offset, Count: int64(limit + 1 + slack)}).Result()
		if err != nil {
			return nil, fmt.Errorf("store: list: %w", err)
		}
		if len(members) == 0 {
			break
		}
		keys := make([]string, len(members))
		for i, m := range members {
			keys[i] = nKey(user, uuid.MustParse(m.Member.(string)))
		}
		vals, err := s.rdb.MGet(ctx, keys...).Result()
		if err != nil {
			return nil, fmt.Errorf("store: list: %w", err)
		}
		for i, m := range members {
			id := m.Member.(string)
			if curID != "" && int64(m.Score) == mustMs(max) && id >= curID {
				continue // kursordan oldin berilgan
			}
			raw, ok := vals[i].(string)
			if !ok {
				dead = append(dead, id) // 30 kunlik TTL tugagan
				continue
			}
			var n Notification
			if json.Unmarshal([]byte(raw), &n) == nil {
				out = append(out, n)
			}
			if len(out) == limit+1 {
				break
			}
		}
		if len(members) < limit+1+slack {
			break
		}
		offset += int64(len(members))
	}
	if len(dead) > 0 {
		_ = s.rdb.ZRem(ctx, zKey(user), dead...).Err()
	}
	return out, nil
}

func mustMs(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }

var ErrNotFound = errors.New("store: not found")

// MarkRead — o'qilgan deb belgilaydi (idempotent). Boshqa foydalanuvchining ID si ErrNotFound beradi.
func (s *Store) MarkRead(ctx context.Context, user, id uuid.UUID) error {
	key := nKey(user, id)
	raw, err := s.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: read: %w", err)
	}
	var n Notification
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		return fmt.Errorf("store: read: %w", err)
	}
	if n.Read {
		return nil
	}
	n.Read = true
	body, _ := json.Marshal(n)
	if err := s.rdb.SetArgs(ctx, key, body, redis.SetArgs{Mode: "XX", KeepTTL: true}).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("store: read: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------- buyurtma -> yo'lovchi

func (s *Store) RememberOrder(ctx context.Context, order, passenger uuid.UUID) error {
	if err := s.rdb.Set(ctx, orderKey(order), passenger.String(), s.opt.TTL).Err(); err != nil {
		return fmt.Errorf("store: order: %w", err)
	}
	return nil
}

// PassengerOf — order.created ko'rilmagan bo'lsa ok=false.
func (s *Store) PassengerOf(ctx context.Context, order uuid.UUID) (uuid.UUID, bool, error) {
	v, err := s.rdb.Get(ctx, orderKey(order)).Result()
	if errors.Is(err, redis.Nil) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("store: order: %w", err)
	}
	id, err := uuid.Parse(v)
	return id, err == nil, nil
}

// ---------------------------------------------------------------- qurilmalar

type Device struct{ Token, Platform string }

var ErrTooManyDevices = errors.New("store: too many devices")

// Token boshqa foydalanuvchida bo'lsa o'sha yerdan olinadi: qurilmaga oldingi egasining push'lari bormaydi.
var registerScript = redis.NewScript(`
local prev = redis.call('GET', KEYS[3])
if prev and prev ~= ARGV[3] then redis.call('HDEL', 'user:' .. prev .. ':devices', ARGV[1]) end
if redis.call('HEXISTS', KEYS[2], ARGV[1]) == 0 and redis.call('HLEN', KEYS[2]) >= tonumber(ARGV[4]) then return 0 end
redis.call('HSET', KEYS[2], ARGV[1], ARGV[2])
redis.call('SET', KEYS[3], ARGV[3])
return 1
`)

func (s *Store) RegisterDevice(ctx context.Context, user uuid.UUID, d Device) error {
	n, err := registerScript.Run(ctx, s.rdb, []string{"", devicesKey(user), deviceOwnerKey(d.Token)},
		d.Token, d.Platform, user.String(), s.opt.MaxDevices).Int()
	if err != nil {
		return fmt.Errorf("store: device: %w", err)
	}
	if n == 0 {
		return ErrTooManyDevices
	}
	return nil
}

func (s *Store) RemoveDevice(ctx context.Context, user uuid.UUID, token string) error {
	pipe := s.rdb.TxPipeline()
	pipe.HDel(ctx, devicesKey(user), token)
	pipe.Del(ctx, deviceOwnerKey(token))
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("store: device: %w", err)
	}
	return nil
}

func (s *Store) Devices(ctx context.Context, user uuid.UUID) ([]Device, error) {
	m, err := s.rdb.HGetAll(ctx, devicesKey(user)).Result()
	if err != nil {
		return nil, fmt.Errorf("store: devices: %w", err)
	}
	out := make([]Device, 0, len(m))
	for t, p := range m {
		out = append(out, Device{Token: t, Platform: p})
	}
	return out, nil
}

// ---------------------------------------------------------------- WebSocket ulanishlar limiti (F-19)

var acquireScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[2]) then return 0 end
redis.call('ZADD', KEYS[1], ARGV[3], ARGV[4])
redis.call('PEXPIRE', KEYS[1], ARGV[5])
return 1
`)

// connLease — ulanish yozuvi shuncha vaqt amal qiladi; ulanish tirik ekan RefreshConn uni uzaytiradi.
// Jarayon qulasa yozuv o'zi tugaydi (limit abadiy band bo'lib qolmaydi).
const ConnLease = 90 * time.Second

func (s *Store) AcquireConn(ctx context.Context, user uuid.UUID, conn uuid.UUID, limit int, now time.Time) (bool, error) {
	n, err := acquireScript.Run(ctx, s.rdb, []string{connsKey(user)},
		now.UnixMilli(), limit, now.Add(ConnLease).UnixMilli(), conn.String(), ConnLease.Milliseconds()*2).Int()
	if err != nil {
		return false, fmt.Errorf("store: ws limit: %w", err)
	}
	return n == 1, nil
}

func (s *Store) RefreshConn(ctx context.Context, user, conn uuid.UUID, now time.Time) error {
	return s.rdb.ZAddXX(ctx, connsKey(user), redis.Z{Score: float64(now.Add(ConnLease).UnixMilli()), Member: conn.String()}).Err()
}

func (s *Store) ReleaseConn(ctx context.Context, user, conn uuid.UUID) error {
	return s.rdb.ZRem(ctx, connsKey(user), conn.String()).Err()
}
