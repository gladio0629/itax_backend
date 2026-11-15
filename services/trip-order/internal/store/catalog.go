package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------- narx takliflari (quotes)

func (s *Store) InsertQuote(ctx context.Context, q domain.Quote) error {
	lines, err := json.Marshal(q.Lines)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO quotes (id, passenger_id, input_hash, price, commission, distance_m, duration_s, lines, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		q.ID, q.PassengerID, q.InputHash, q.Price, q.Commission, q.DistanceM, q.DurationS, lines, trunc(q.ExpiresAt), trunc(q.CreatedAt))
	return err
}

func (s *Store) GetQuote(ctx context.Context, id uuid.UUID) (domain.Quote, error) {
	var q domain.Quote
	var lines []byte
	err := s.pool.QueryRow(ctx, `SELECT id, passenger_id, input_hash, price, commission, distance_m, duration_s, lines, expires_at, created_at, used_order_id
		FROM quotes WHERE id=$1`, id).Scan(&q.ID, &q.PassengerID, &q.InputHash, &q.Price, &q.Commission, &q.DistanceM, &q.DurationS,
		&lines, &q.ExpiresAt, &q.CreatedAt, &q.UsedOrderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Quote{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Quote{}, err
	}
	if err := json.Unmarshal(lines, &q.Lines); err != nil {
		return domain.Quote{}, fmt.Errorf("quote lines: %w", err)
	}
	q.ExpiresAt, q.CreatedAt = q.ExpiresAt.UTC(), q.CreatedAt.UTC()
	return q, nil
}

// PurgeQuotes — muddati bir kundan oldin o'tgan, ishlatilmagan takliflarni o'chiradi.
func (s *Store) PurgeQuotes(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM quotes WHERE used_order_id IS NULL AND expires_at < $1`, trunc(now.Add(-24*time.Hour)))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ---------------------------------------------------------------- shaharlar va manzillar

const cityCols = `id, slug, name, region, lat, lng`

func scanCity(row pgx.Row) (domain.City, error) {
	var c domain.City
	err := row.Scan(&c.ID, &c.Slug, &c.Name, &c.Region, &c.Lat, &c.Lng)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.City{}, domain.ErrNotFound
	}
	return c, err
}

// ListCities — faol shaharlar (sort, nom bo'yicha).
func (s *Store) ListCities(ctx context.Context) ([]domain.City, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cityCols+` FROM cities WHERE active ORDER BY sort, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.City
	for rows.Next() {
		c, err := scanCity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCity(ctx context.Context, id uuid.UUID) (domain.City, error) {
	return scanCity(s.pool.QueryRow(ctx, `SELECT `+cityCols+` FROM cities WHERE id=$1 AND active`, id))
}

// SearchPlaces — gazetteer manzillari: search_key ichida key bor (key allaqachon normallashtirilgan). cityID bo'lsa faqat shu shahar.
func (s *Store) SearchPlaces(ctx context.Context, key string, cityID *uuid.UUID, limit int) ([]domain.Place, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.city_id, p.name, CASE WHEN p.area = '' THEN c.name ELSE p.area || ', ' || c.name END, p.kind, p.lat, p.lng, p.entrance
		FROM places p JOIN cities c ON c.id = p.city_id
		WHERE c.active AND p.search_key LIKE '%' || $1 || '%' AND ($2::uuid IS NULL OR p.city_id = $2)
		ORDER BY (p.search_key LIKE $1 || '%') DESC, p.sort, p.name LIMIT $3`, key, cityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPlaces(rows)
}

// PlacesNear — nuqtaga eng yaqin gazetteer manzillari (taxminiy: kvadrat ichida, so'ng Go'da masofa bo'yicha saralanadi).
func (s *Store) PlacesNear(ctx context.Context, lat, lng, deg float64) ([]domain.Place, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.city_id, p.name, CASE WHEN p.area = '' THEN c.name ELSE p.area || ', ' || c.name END, p.kind, p.lat, p.lng, p.entrance
		FROM places p JOIN cities c ON c.id = p.city_id
		WHERE c.active AND p.lat BETWEEN $1::float8 - $3::float8 AND $1::float8 + $3::float8
		  AND p.lng BETWEEN $2::float8 - $3::float8 AND $2::float8 + $3::float8 LIMIT 200`, lat, lng, deg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPlaces(rows)
}

func scanPlaces(rows pgx.Rows) ([]domain.Place, error) {
	var out []domain.Place
	for rows.Next() {
		var p domain.Place
		if err := rows.Scan(&p.ID, &p.CityID, &p.Name, &p.Area, &p.Kind, &p.Lat, &p.Lng, &p.Entrance); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- saqlangan manzillar

const savedCols = `id, user_id, name, area, lat, lng, details, created_at`

func scanSaved(row pgx.Row) (domain.SavedPlace, error) {
	var p domain.SavedPlace
	var details []byte
	err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Area, &p.Lat, &p.Lng, &details, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SavedPlace{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.SavedPlace{}, err
	}
	if len(details) > 0 {
		if err := json.Unmarshal(details, &p.Details); err != nil {
			return domain.SavedPlace{}, err
		}
	}
	p.CreatedAt = p.CreatedAt.UTC()
	return p, nil
}

func (s *Store) ListSavedPlaces(ctx context.Context, user uuid.UUID) ([]domain.SavedPlace, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+savedCols+` FROM saved_places WHERE user_id=$1 ORDER BY created_at, id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SavedPlace
	for rows.Next() {
		p, err := scanSaved(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateSavedPlace — foydalanuvchi bo'yicha qulf ostida sanab qo'shadi (limit oshmaydi). ErrLimitReached — limitda.
func (s *Store) CreateSavedPlace(ctx context.Context, p domain.SavedPlace, limit int) (domain.SavedPlace, error) {
	var out domain.SavedPlace
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockUser(ctx, tx, "sp", p.UserID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM saved_places WHERE user_id=$1`, p.UserID).Scan(&n); err != nil {
			return err
		}
		if n >= limit {
			return domain.ErrLimitReached
		}
		var details []byte
		if len(p.Details) > 0 {
			var err error
			if details, err = json.Marshal(p.Details); err != nil {
				return err
			}
		}
		var err error
		out, err = scanSaved(tx.QueryRow(ctx, `INSERT INTO saved_places (id, user_id, name, area, lat, lng, details, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+savedCols,
			p.ID, p.UserID, p.Name, p.Area, p.Lat, p.Lng, details, trunc(p.CreatedAt)))
		return err
	})
	return out, err
}

// DeleteSavedPlace — faqat egasining yozuvi; yo'q bo'lsa ErrNotFound.
func (s *Store) DeleteSavedPlace(ctx context.Context, user, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM saved_places WHERE id=$1 AND user_id=$2`, id, user)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------- mashhur yo'nalishlar

func (s *Store) PopularRoutes(ctx context.Context) ([]domain.PopularRoute, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, a.id, a.slug, a.name, a.region, a.lat, a.lng, b.id, b.slug, b.name, b.region, b.lat, b.lng
		FROM popular_routes r JOIN cities a ON a.id = r.from_city_id JOIN cities b ON b.id = r.to_city_id
		WHERE r.active AND a.active AND b.active ORDER BY r.sort, a.name, b.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.PopularRoute
	for rows.Next() {
		var r domain.PopularRoute
		if err := rows.Scan(&r.ID, &r.From.ID, &r.From.Slug, &r.From.Name, &r.From.Region, &r.From.Lat, &r.From.Lng,
			&r.To.ID, &r.To.Slug, &r.To.Name, &r.To.Region, &r.To.Lat, &r.To.Lng); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
