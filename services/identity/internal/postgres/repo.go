package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/identity/internal/auth"
	"github.com/izgo/identity/internal/events"
	"github.com/izgo/shared/roles"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repo — auth.Repo va events.Outbox ning PostgreSQL implementatsiyasi.
type Repo struct{ pool *pgxpool.Pool }

var (
	_ auth.Repo     = (*Repo)(nil)
	_ events.Outbox = (*Repo)(nil)
)

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

const userCols = `id, phone, role, first_name, last_name, settings, rating, created_at`

func scanUser(row pgx.Row) (auth.User, error) {
	var (
		u        auth.User
		role     string
		settings []byte
	)
	if err := row.Scan(&u.ID, &u.Phone, &role, &u.FirstName, &u.LastName, &settings, &u.Rating, &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.User{}, auth.ErrNotFound
		}
		return auth.User{}, err
	}
	u.Role = roles.Role(role)
	u.Settings = auth.DefaultSettings() // saqlanmagan maydonlar default qiymat bilan qoladi
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &u.Settings); err != nil {
			return auth.User{}, fmt.Errorf("settings: %w", err)
		}
	}
	return u, nil
}

func (r *Repo) FindOrCreateUser(ctx context.Context, cand auth.User, newEvent func(auth.User) (events.Event, error)) (auth.User, bool, error) {
	settings, err := json.Marshal(cand.Settings)
	if err != nil {
		return auth.User{}, false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return auth.User{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	u, err := scanUser(tx.QueryRow(ctx, `
		INSERT INTO users (id, phone, role, settings, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (phone, role) DO NOTHING
		RETURNING `+userCols,
		cand.ID, cand.Phone, string(cand.Role), settings, cand.CreatedAt))
	created := err == nil
	if errors.Is(err, auth.ErrNotFound) { // ON CONFLICT: allaqachon bor
		u, err = scanUser(tx.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE phone = $1 AND role = $2`,
			cand.Phone, string(cand.Role)))
	}
	if err != nil {
		return auth.User{}, false, err
	}

	if created {
		ev, err := newEvent(u)
		if err != nil {
			return auth.User{}, false, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO events_outbox (id, subject, payload, created_at) VALUES ($1, $2, $3, $4)`,
			ev.ID, ev.Subject, ev.Payload, ev.CreatedAt); err != nil {
			return auth.User{}, false, fmt.Errorf("outbox: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.User{}, false, err
	}
	return u, created, nil
}

func (r *Repo) GetUser(ctx context.Context, id uuid.UUID) (auth.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (r *Repo) UpdateUserName(ctx context.Context, id uuid.UUID, first, last *string) (auth.User, error) {
	return scanUser(r.pool.QueryRow(ctx, `
		UPDATE users SET
			first_name = COALESCE($2, first_name),
			last_name  = COALESCE($3, last_name),
			updated_at = now()
		WHERE id = $1
		RETURNING `+userCols, id, first, last))
}

func (r *Repo) UpdateUserSettings(ctx context.Context, id uuid.UUID, s auth.Settings) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `UPDATE users SET settings = $2, updated_at = now() WHERE id = $1`, id, b)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrNotFound
	}
	return nil
}

func (r *Repo) InsertRefresh(ctx context.Context, t auth.RefreshToken) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO refresh_tokens (jti, user_id, family_id, expires_at) VALUES ($1, $2, $3, $4)`,
		t.JTI, t.UserID, t.Family, t.ExpiresAt)
	return err
}

func (r *Repo) RotateRefresh(ctx context.Context, old uuid.UUID, next auth.RefreshToken) (auth.RotateOutcome, uuid.UUID, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return auth.Rejected, uuid.Nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Atomik "iste'mol qilish": parallel ikkita so'rovdan faqat bittasi qator oladi.
	var owner, family uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE refresh_tokens SET used_at = now()
		WHERE jti = $1 AND used_at IS NULL AND revoked_at IS NULL AND expires_at > now()
		RETURNING user_id, family_id`, old).Scan(&owner, &family)
	if errors.Is(err, pgx.ErrNoRows) {
		var usedAt *time.Time
		var userID uuid.UUID
		qerr := tx.QueryRow(ctx, `SELECT user_id, used_at FROM refresh_tokens WHERE jti = $1`, old).Scan(&userID, &usedAt)
		if errors.Is(qerr, pgx.ErrNoRows) {
			return auth.Rejected, uuid.Nil, nil
		}
		if qerr != nil {
			return auth.Rejected, uuid.Nil, qerr
		}
		if usedAt != nil {
			return auth.Reused, userID, nil
		}
		return auth.Rejected, uuid.Nil, nil // bekor qilingan yoki muddati tugagan
	}
	if err != nil {
		return auth.Rejected, uuid.Nil, err
	}
	// Imzolangan token'dagi subject/sid bazadagi qator bilan mos bo'lishi shart.
	if owner != next.UserID || family != next.Family {
		return auth.Rejected, uuid.Nil, nil
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (jti, user_id, family_id, expires_at) VALUES ($1, $2, $3, $4)`,
		next.JTI, next.UserID, next.Family, next.ExpiresAt); err != nil {
		return auth.Rejected, uuid.Nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.Rejected, uuid.Nil, err
	}
	return auth.Rotated, owner, nil
}

func (r *Repo) RevokeFamily(ctx context.Context, family uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE family_id = $1 AND revoked_at IS NULL`, family)
	return err
}

func (r *Repo) RevokeAllForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `
		UPDATE refresh_tokens SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL
		RETURNING family_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[uuid.UUID]struct{}{}
	var out []uuid.UUID
	for rows.Next() {
		var f uuid.UUID
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		if _, ok := seen[f]; !ok {
			seen[f] = struct{}{}
			out = append(out, f)
		}
	}
	return out, rows.Err()
}

// PurgeExpiredRefresh — muddati o'tganiga 1 kundan oshgan refresh yozuvlarini tozalaydi.
func (r *Repo) PurgeExpiredRefresh(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < now() - interval '1 day'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

const staffCols = `id, email, phone, full_name, role, is_active`

// uniqueViolation — PostgreSQL SQLSTATE 23505.
const uniqueViolation = "23505"

func scanStaff(row pgx.Row) (auth.Staff, error) {
	var s auth.Staff
	var role string
	if err := row.Scan(&s.ID, &s.Email, &s.Phone, &s.FullName, &role, &s.Active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Staff{}, auth.ErrNotFound
		}
		return auth.Staff{}, err
	}
	s.Role = roles.Role(role)
	return s, nil
}

func (r *Repo) GetStaffByEmail(ctx context.Context, email string) (auth.Staff, error) {
	return scanStaff(r.pool.QueryRow(ctx, `SELECT `+staffCols+` FROM staff_users WHERE lower(email) = lower($1)`, email))
}

func (r *Repo) GetStaffByPhone(ctx context.Context, phone string) (auth.Staff, error) {
	return scanStaff(r.pool.QueryRow(ctx, `SELECT `+staffCols+` FROM staff_users WHERE phone = $1`, phone))
}

func (r *Repo) GetStaff(ctx context.Context, id uuid.UUID) (auth.Staff, error) {
	return scanStaff(r.pool.QueryRow(ctx, `SELECT `+staffCols+` FROM staff_users WHERE id = $1`, id))
}

func (r *Repo) TouchStaffLogin(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE staff_users SET last_login_at = $2 WHERE id = $1`, id, at)
	return err
}

// EnsureStaff — maqsadsiz ON CONFLICT: email (staff_users_email_key) yoki telefon (staff_users_phone_key)
// band bo'lsa yozuv qo'shilmaydi.
func (r *Repo) EnsureStaff(ctx context.Context, s auth.Staff) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO staff_users (id, email, phone, full_name, role, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING`,
		s.ID, s.Email, s.Phone, s.FullName, string(s.Role), s.Active)
	return err
}

func (r *Repo) CreateStaff(ctx context.Context, s auth.Staff) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO staff_users (id, email, phone, full_name, role, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		s.ID, s.Email, s.Phone, s.FullName, string(s.Role), s.Active)
	return mapStaffUnique(err)
}

func (r *Repo) UpdateStaff(ctx context.Context, id uuid.UUID, apply func(*auth.Staff) error) (auth.Staff, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return auth.Staff{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	s, err := scanStaff(tx.QueryRow(ctx, `SELECT `+staffCols+` FROM staff_users WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return auth.Staff{}, err
	}
	if err := apply(&s); err != nil {
		return auth.Staff{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE staff_users SET email = $2, phone = $3, full_name = $4, role = $5, is_active = $6 WHERE id = $1`,
		s.ID, s.Email, s.Phone, s.FullName, string(s.Role), s.Active); err != nil {
		return auth.Staff{}, mapStaffUnique(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.Staff{}, err
	}
	return s, nil
}

// mapStaffUnique — staff_users unikal indekslari buzilishini domen xatosiga aylantiradi.
func mapStaffUnique(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == uniqueViolation {
		switch pe.ConstraintName {
		case "staff_users_email_key":
			return auth.ErrStaffEmailTaken
		case "staff_users_phone_key":
			return auth.ErrStaffPhoneTaken
		}
	}
	return err
}

// DrainOutbox — yuborilmagan hodisalarni (SKIP LOCKED: replikalar bir-biriga xalaqit bermaydi)
// ketma-ket publish qiladi. Muvaffaqiyatli yuborilganlar publish xatosida ham saqlanadi.
func (r *Repo) DrainOutbox(ctx context.Context, limit int, publish func(context.Context, events.Event) error) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id, subject, payload, created_at FROM events_outbox
		WHERE published_at IS NULL ORDER BY created_at, id
		LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return 0, err
	}
	var batch []events.Event
	for rows.Next() {
		var e events.Event
		if err := rows.Scan(&e.ID, &e.Subject, &e.Payload, &e.CreatedAt); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	sent := 0
	var pubErr error
	for _, e := range batch {
		if pubErr = publish(ctx, e); pubErr != nil {
			break
		}
		if _, err := tx.Exec(ctx, `UPDATE events_outbox SET published_at = now() WHERE id = $1`, e.ID); err != nil {
			return 0, err
		}
		sent++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	if pubErr != nil {
		return sent, pubErr
	}
	return sent, nil
}
