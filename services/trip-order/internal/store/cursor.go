package store

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Cursor — (created_at, id) bo'yicha keyingi sahifa nuqtasi. Klientga noshaffof satr sifatida beriladi.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

var ErrBadCursor = errors.New("store: bad cursor")

func (c Cursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%s", c.At.UnixMicro(), c.ID)))
}

func DecodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrBadCursor
	}
	micro, id, ok := strings.Cut(string(raw), ":")
	if !ok {
		return Cursor{}, ErrBadCursor
	}
	us, err := strconv.ParseInt(micro, 10, 64)
	if err != nil {
		return Cursor{}, ErrBadCursor
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return Cursor{}, ErrBadCursor
	}
	return Cursor{At: time.UnixMicro(us).UTC(), ID: u}, nil
}
