package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	c := Cursor{At: time.Date(2026, 11, 11, 12, 30, 45, 123456000, time.UTC), ID: uuid.New()}
	got, err := DecodeCursor(c.Encode())
	if err != nil || !got.At.Equal(c.At) || got.ID != c.ID {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{"", "!!", "bm90YXVVaWQ", "MTIzOm5vdC11dWlk", "YWJjOjAwMDAwMDAwLTAwMDAtMDAwMC0wMDAwLTAwMDAwMDAwMDAwMA"} {
		if _, err := DecodeCursor(bad); err == nil {
			t.Errorf("%q: xato kutilgan", bad)
		}
	}
}
