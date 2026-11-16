// Package memrepo — auth.Repo ning xotiradagi implementatsiyasi (faqat testlar uchun).
// Semantika PostgreSQL repo bilan bir xil: bir martalik refresh, reuse aniqlash, outbox.
package memrepo

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/identity/internal/auth"
	"github.com/izgo/identity/internal/events"
)

type refresh struct {
	auth.RefreshToken
	used, revoked bool
}

type Repo struct {
	mu      sync.Mutex
	users   map[uuid.UUID]auth.User
	staff   map[uuid.UUID]auth.Staff
	refresh map[uuid.UUID]*refresh
	Outbox  []events.Event
}

var _ auth.Repo = (*Repo)(nil)

func New() *Repo {
	return &Repo{users: map[uuid.UUID]auth.User{}, staff: map[uuid.UUID]auth.Staff{}, refresh: map[uuid.UUID]*refresh{}}
}

func (r *Repo) FindOrCreateUser(_ context.Context, cand auth.User, newEvent func(auth.User) (events.Event, error)) (auth.User, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.Phone == cand.Phone && u.Role == cand.Role {
			return u, false, nil
		}
	}
	ev, err := newEvent(cand)
	if err != nil {
		return auth.User{}, false, err
	}
	r.users[cand.ID] = cand
	r.Outbox = append(r.Outbox, ev)
	return cand, true, nil
}

func (r *Repo) GetUser(_ context.Context, id uuid.UUID) (auth.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return auth.User{}, auth.ErrNotFound
	}
	return u, nil
}

func (r *Repo) UpdateUserName(_ context.Context, id uuid.UUID, first, last *string) (auth.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return auth.User{}, auth.ErrNotFound
	}
	if first != nil {
		u.FirstName = first
	}
	if last != nil {
		u.LastName = last
	}
	r.users[id] = u
	return u, nil
}

func (r *Repo) UpdateUserSettings(_ context.Context, id uuid.UUID, s auth.Settings) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return auth.ErrNotFound
	}
	u.Settings = s
	r.users[id] = u
	return nil
}

func (r *Repo) InsertRefresh(_ context.Context, t auth.RefreshToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refresh[t.JTI] = &refresh{RefreshToken: t}
	return nil
}

func (r *Repo) RotateRefresh(_ context.Context, old uuid.UUID, next auth.RefreshToken) (auth.RotateOutcome, uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.refresh[old]
	if !ok {
		return auth.Rejected, uuid.Nil, nil
	}
	if t.used {
		return auth.Reused, t.UserID, nil
	}
	if t.revoked || !t.ExpiresAt.After(time.Now()) || t.UserID != next.UserID || t.Family != next.Family {
		return auth.Rejected, uuid.Nil, nil
	}
	t.used = true
	r.refresh[next.JTI] = &refresh{RefreshToken: next}
	return auth.Rotated, t.UserID, nil
}

func (r *Repo) RevokeFamily(_ context.Context, family uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.refresh {
		if t.Family == family {
			t.revoked = true
		}
	}
	return nil
}

func (r *Repo) RevokeAllForUser(_ context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, t := range r.refresh {
		if t.UserID == userID && !t.revoked {
			t.revoked = true
			if !seen[t.Family] {
				seen[t.Family] = true
				out = append(out, t.Family)
			}
		}
	}
	return out, nil
}

// LiveRefreshCount — bekor qilinmagan va ishlatilmagan refresh tokenlar soni (testlar uchun).
func (r *Repo) LiveRefreshCount(userID uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, t := range r.refresh {
		if t.UserID == userID && !t.revoked && !t.used {
			n++
		}
	}
	return n
}

func (r *Repo) GetStaffByEmail(_ context.Context, email string) (auth.Staff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.staff {
		if s.Email != nil && strings.EqualFold(*s.Email, email) {
			return s, nil
		}
	}
	return auth.Staff{}, auth.ErrNotFound
}

func (r *Repo) GetStaffByPhone(_ context.Context, phone string) (auth.Staff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.staff {
		if s.Phone == phone {
			return s, nil
		}
	}
	return auth.Staff{}, auth.ErrNotFound
}

func (r *Repo) GetStaff(_ context.Context, id uuid.UUID) (auth.Staff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.staff[id]
	if !ok {
		return auth.Staff{}, auth.ErrNotFound
	}
	return s, nil
}

func (r *Repo) TouchStaffLogin(context.Context, uuid.UUID, time.Time) error { return nil }

func (r *Repo) InsertEvent(_ context.Context, e events.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.Outbox {
		if x.ID == e.ID {
			return nil
		}
	}
	r.Outbox = append(r.Outbox, e)
	return nil
}

func (r *Repo) UpdateStaff(_ context.Context, id uuid.UUID, apply func(*auth.Staff) error) (auth.Staff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.staff[id]
	if !ok {
		return auth.Staff{}, auth.ErrNotFound
	}
	s := cur
	if cur.Email != nil { // apply ko'rsatkichni o'zgartirsa, saqlangan yozuv buzilmasin
		e := *cur.Email
		s.Email = &e
	}
	if err := apply(&s); err != nil {
		return auth.Staff{}, err
	}
	if err := r.conflict(s); err != nil {
		return auth.Staff{}, err
	}
	r.staff[id] = s
	return s, nil
}

// conflict — PostgreSQL dagi unikal indekslar bilan bir xil: lower(email) (NULL lar to'qnashmaydi) va phone.
// O'zi bilan (bir xil ID) to'qnashuv hisoblanmaydi.
func (r *Repo) conflict(s auth.Staff) error {
	for _, e := range r.staff {
		if e.ID == s.ID {
			continue
		}
		if s.Email != nil && e.Email != nil && strings.EqualFold(*e.Email, *s.Email) {
			return auth.ErrStaffEmailTaken
		}
		if e.Phone == s.Phone {
			return auth.ErrStaffPhoneTaken
		}
	}
	return nil
}

func (r *Repo) EnsureStaff(_ context.Context, s auth.Staff) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conflict(s) != nil {
		return nil
	}
	r.staff[s.ID] = s
	return nil
}

func (r *Repo) CreateStaff(_ context.Context, s auth.Staff) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.conflict(s); err != nil {
		return err
	}
	r.staff[s.ID] = s
	return nil
}
