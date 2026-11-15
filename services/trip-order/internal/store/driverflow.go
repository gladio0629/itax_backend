package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/izgo/trip-order/internal/events"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------- taklifni rad etish

// RejectOffer: pending -> rejected (faqat o'z taklifi). Takroriy rad etish xato emas.
// ErrNotFound — taklif yo'q/begona; *domain.StateError — taklif allaqachon qabul qilingan yoki yopilgan.
func (s *Store) RejectOffer(ctx context.Context, offerID, driver uuid.UUID, reason *string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE offers SET status='rejected', reject_reason=$3 WHERE id=$1 AND driver_id=$2 AND status='pending'`,
		offerID, driver, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var st string
	err = s.pool.QueryRow(ctx, `SELECT status FROM offers WHERE id=$1 AND driver_id=$2`, offerID, driver).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if domain.OfferStatus(st) == domain.OfferRejected {
		return nil
	}
	return &domain.StateError{}
}

// ---------------------------------------------------------------- yetib keldim

// ArriveTrip: assigned (arrived_at yo'q) -> arrived_at; buyurtma accepted -> arrived; order.arrived outbox'ga.
// changed=false — allaqachon belgilangan (takroriy chaqiruv, hodisa yozilmaydi).
func (s *Store) ArriveTrip(ctx context.Context, tripID, driver uuid.UUID, now time.Time,
	mkEvent func(domain.Trip) (events.Event, error)) (domain.Trip, bool, error) {
	var out domain.Trip
	changed := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		t, err := scanTrip(tx.QueryRow(ctx, `UPDATE trips SET arrived_at=$3
			WHERE id=$1 AND driver_id=$2 AND status='assigned' AND arrived_at IS NULL RETURNING `+tripCols, tripID, driver, trunc(now)))
		if errors.Is(err, domain.ErrNotFound) {
			cur, e := scanTrip(tx.QueryRow(ctx, `SELECT `+tripCols+` FROM trips WHERE id=$1 AND driver_id=$2`, tripID, driver))
			if e != nil {
				return e
			}
			if cur.Status == domain.TripAssigned && cur.ArrivedAt != nil {
				out = cur
				return nil
			}
			return &domain.StateError{Trip: cur.Status}
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE orders SET status='arrived', version=version+1, updated_at=$2 WHERE id=$1 AND status='accepted'`,
			t.OrderID, trunc(now))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return &domain.StateError{Trip: t.Status}
		}
		ev, err := mkEvent(t)
		if err != nil {
			return err
		}
		if err := insertOutbox(ctx, tx, ev); err != nil {
			return err
		}
		out, changed = t, true
		return nil
	})
	return out, changed, err
}

// ---------------------------------------------------------------- haydovchi safarni bekor qiladi

// DriverCancel — haydovchi boshlanmagan safarni bekor qiladi: safar cancelled, buyurtma qayta searching (boshqa
// haydovchilarga takliflar), shu haydovchining taklifi yopiladi; trip.cancelled outbox'ga.
// *domain.StateError — safar boshlangan/tugagan/bekor qilingan.
func (s *Store) DriverCancel(ctx context.Context, tripID, driver uuid.UUID, reason string, comment *string, now, searchExpires time.Time,
	offers []domain.Offer, mkEvent func(domain.Trip) (events.Event, error)) (domain.Trip, error) {
	var out domain.Trip
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockUser(ctx, tx, "d", driver); err != nil {
			return err
		}
		note := reason
		if comment != nil && *comment != "" {
			note = reason + ": " + *comment
		}
		t, err := scanTrip(tx.QueryRow(ctx, `UPDATE trips SET status='cancelled', cancelled_at=$3, cancelled_by='driver', cancel_reason=$4
			WHERE id=$1 AND driver_id=$2 AND status='assigned' RETURNING `+tripCols, tripID, driver, trunc(now), note))
		if errors.Is(err, domain.ErrNotFound) {
			cur, e := scanTrip(tx.QueryRow(ctx, `SELECT `+tripCols+` FROM trips WHERE id=$1 AND driver_id=$2`, tripID, driver))
			if e != nil {
				return e
			}
			return &domain.StateError{Trip: cur.Status}
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE orders SET status='searching', driver_id=NULL, trip_id=NULL, search_expires_at=$3,
				version=version+1, updated_at=$2
			WHERE id=$1 AND status IN ('accepted','arrived')`, t.OrderID, trunc(now), trunc(searchExpires))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return &domain.StateError{Trip: t.Status}
		}
		if _, err := tx.Exec(ctx, `UPDATE offers SET status='closed' WHERE order_id=$1 AND driver_id=$2`, t.OrderID, driver); err != nil {
			return err
		}
		for _, of := range offers {
			// avval yopilgan taklif qayta ochiladi; rad etgan haydovchiga qayta yuborilmaydi
			if _, err := tx.Exec(ctx, `INSERT INTO offers (id, order_id, driver_id, pickup_eta_s, status, expires_at, created_at)
				VALUES ($1,$2,$3,$4,'pending',$5,$6)
				ON CONFLICT (order_id, driver_id) DO UPDATE SET status='pending', expires_at=EXCLUDED.expires_at,
					created_at=EXCLUDED.created_at, pickup_eta_s=EXCLUDED.pickup_eta_s
				WHERE offers.status='closed'`,
				of.ID, of.OrderID, of.DriverID, of.PickupETAs, trunc(of.ExpiresAt), trunc(of.CreatedAt)); err != nil {
				return fmt.Errorf("offer: %w", err)
			}
		}
		ev, err := mkEvent(t)
		if err != nil {
			return err
		}
		if err := insertOutbox(ctx, tx, ev); err != nil {
			return err
		}
		out = t
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------- admin bekor qilishi

// AdminCancel — searching/accepted/arrived -> cancelled (cancelled_by=admin); biriktirilgan safar ham cancelled.
// changed=false — allaqachon bekor qilingan. trip — bekor qilingan safar (bo'lsa). *domain.StateError — boshqa holatlarda.
func (s *Store) AdminCancel(ctx context.Context, orderID uuid.UUID, reason string, comment *string, now time.Time,
	mkEvents func(o domain.Order, trip *domain.Trip) ([]events.Event, error)) (domain.Order, *domain.Trip, bool, error) {
	var (
		out     domain.Order
		trip    *domain.Trip
		changed bool
	)
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		cur, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderColList+` FROM orders WHERE id=$1 FOR UPDATE`, orderID))
		if err != nil {
			return err
		}
		switch {
		case cur.Status == domain.Cancelled:
			out = cur
			return nil
		case cur.Status != domain.Searching && !cur.Status.Assigned():
			return &domain.StateError{Order: cur.Status}
		}
		if cur.TripID != nil {
			t, err := scanTrip(tx.QueryRow(ctx, `UPDATE trips SET status='cancelled', cancelled_at=$2, cancelled_by='admin', cancel_reason=$3
				WHERE id=$1 AND status='assigned' RETURNING `+tripCols, *cur.TripID, trunc(now), reason))
			if err != nil {
				return err
			}
			trip = &t
		}
		o, err := scanOrder(tx.QueryRow(ctx, `UPDATE orders SET status='cancelled', cancel_reason=$2, cancel_comment=$3, cancelled_by='admin',
				version=version+1, updated_at=$4
			WHERE id=$1 RETURNING `+orderColList, orderID, reason, comment, trunc(now)))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE offers SET status='closed' WHERE order_id=$1 AND status='pending'`, orderID); err != nil {
			return err
		}
		evs, err := mkEvents(o, trip)
		if err != nil {
			return err
		}
		for _, ev := range evs {
			if err := insertOutbox(ctx, tx, ev); err != nil {
				return err
			}
		}
		out, changed = o, true
		return nil
	})
	return out, trip, changed, err
}

// AdminUpdate — faqat searching holatida, narxga ta'sir qilmaydigan maydonlar (izoh, to'lov, vaqt). false — holat yoki versiya o'zgargan.
func (s *Store) AdminUpdate(ctx context.Context, o domain.Order, expectedVersion int, now time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE orders SET comment=$3, payment_method=$4, scheduled_at=$5, scheduled_from=$6, scheduled_to=$7,
			search_expires_at=$8, version=version+1, updated_at=$9
		WHERE id=$1 AND status='searching' AND version=$2`,
		o.ID, expectedVersion, o.Comment, string(o.PaymentMethod), o.ScheduledAt, o.ScheduledFrom, o.ScheduledTo, trunc(o.SearchExpiresAt), trunc(now))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	_, err = s.pool.Exec(ctx, `UPDATE offers SET expires_at=$2 WHERE order_id=$1 AND status='pending'`, o.ID, trunc(o.SearchExpiresAt))
	return true, err
}

// ---------------------------------------------------------------- haydovchi safarlari

// ListTrips — haydovchi safarlari (yangidan eskiga) buyurtmasi bilan; statuses bo'sh bo'lmasligi kerak.
func (s *Store) ListTrips(ctx context.Context, driver uuid.UUID, statuses []string, cur *Cursor, limit int) ([]domain.TripView, error) {
	var at *time.Time
	var id *uuid.UUID
	if cur != nil {
		at, id = &cur.At, &cur.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+tripColsAlias("t")+`, `+orderCols("o")+`
		FROM trips t JOIN orders o ON o.id = t.order_id
		WHERE t.driver_id=$1 AND t.status = ANY($2)
		  AND ($3::timestamptz IS NULL OR (t.created_at, t.id) < ($3::timestamptz, $4::uuid))
		ORDER BY t.created_at DESC, t.id DESC LIMIT $5`, driver, statuses, at, id, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TripView
	for rows.Next() {
		var v domain.TripView
		var st string
		var sc orderScan
		if err := rows.Scan(append(tripDest(&v.Trip, &st), sc.dest(&v.Order)...)...); err != nil {
			return nil, err
		}
		v.Trip.Status = domain.TripStatus(st)
		v.Trip.CreatedAt = v.Trip.CreatedAt.UTC()
		if err := sc.finish(&v.Order); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func tripColsAlias(alias string) string {
	out := ""
	for i, c := range splitCols(tripCols) {
		if i > 0 {
			out += ", "
		}
		out += alias + "." + c
	}
	return out
}

// ---------------------------------------------------------------- admin: ro'yxat va statistika

type OrderFilter struct {
	Status    *string
	From, To  *time.Time // [From, To)
	Passenger *uuid.UUID
}

// ListAllOrders — barcha buyurtmalar (admin), yangidan eskiga; limit+1 tagacha.
func (s *Store) ListAllOrders(ctx context.Context, f OrderFilter, cur *Cursor, limit int) ([]domain.Order, error) {
	var at *time.Time
	var id *uuid.UUID
	if cur != nil {
		at, id = &cur.At, &cur.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+orderColList+` FROM orders
		WHERE ($1::text IS NULL OR status = $1) AND ($2::timestamptz IS NULL OR created_at >= $2) AND ($3::timestamptz IS NULL OR created_at < $3)
		  AND ($4::uuid IS NULL OR passenger_id = $4)
		  AND ($5::timestamptz IS NULL OR (created_at, id) < ($5::timestamptz, $6::uuid))
		ORDER BY created_at DESC, id DESC LIMIT $7`, f.Status, f.From, f.To, f.Passenger, at, id, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type DayStat struct {
	Date      string
	Orders    int
	Completed int
	Revenue   int64
}

type RouteStat struct {
	FromCityID, ToCityID uuid.UUID
	FromName, ToName     string
	Orders               int
}

type Stats struct {
	ByStatus   map[string]int
	Total      int
	Revenue    int64 // yakunlangan buyurtmalar narxi yig'indisi
	Commission int64
	Daily      []DayStat
	TopRoutes  []RouteStat
}

// OrderStats — [from, to) oralig'ida yaratilgan buyurtmalar bo'yicha. tzOffset — kunlarni guruhlash uchun (UTC+5).
func (s *Store) OrderStats(ctx context.Context, from, to *time.Time, tzOffset time.Duration) (Stats, error) {
	st := Stats{ByStatus: map[string]int{}}
	const where = `($1::timestamptz IS NULL OR created_at >= $1) AND ($2::timestamptz IS NULL OR created_at < $2)`
	rows, err := s.pool.Query(ctx, `SELECT status, count(*), COALESCE(sum(price) FILTER (WHERE status='completed'),0),
			COALESCE(sum(commission) FILTER (WHERE status='completed'),0)
		FROM orders WHERE `+where+` GROUP BY status`, from, to)
	if err != nil {
		return Stats{}, err
	}
	for rows.Next() {
		var status string
		var n int
		var rev, com int64
		if err := rows.Scan(&status, &n, &rev, &com); err != nil {
			rows.Close()
			return Stats{}, err
		}
		st.ByStatus[status], st.Total, st.Revenue, st.Commission = n, st.Total+n, st.Revenue+rev, st.Commission+com
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Stats{}, err
	}

	secs := int(tzOffset.Seconds())
	rows, err = s.pool.Query(ctx, `SELECT to_char((created_at + make_interval(secs => $3)) AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS d,
			count(*), count(*) FILTER (WHERE status='completed'), COALESCE(sum(price) FILTER (WHERE status='completed'),0)
		FROM orders WHERE `+where+` GROUP BY d ORDER BY d`, from, to, secs)
	if err != nil {
		return Stats{}, err
	}
	for rows.Next() {
		var d DayStat
		if err := rows.Scan(&d.Date, &d.Orders, &d.Completed, &d.Revenue); err != nil {
			rows.Close()
			return Stats{}, err
		}
		st.Daily = append(st.Daily, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Stats{}, err
	}

	rows, err = s.pool.Query(ctx, `SELECT o.from_city_id, o.to_city_id, a.name, b.name, count(*) AS n
		FROM orders o JOIN cities a ON a.id = o.from_city_id JOIN cities b ON b.id = o.to_city_id
		WHERE ($1::timestamptz IS NULL OR o.created_at >= $1) AND ($2::timestamptz IS NULL OR o.created_at < $2)
		GROUP BY o.from_city_id, o.to_city_id, a.name, b.name ORDER BY n DESC, a.name, b.name LIMIT 10`, from, to)
	if err != nil {
		return Stats{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var r RouteStat
		if err := rows.Scan(&r.FromCityID, &r.ToCityID, &r.FromName, &r.ToName, &r.Orders); err != nil {
			return Stats{}, err
		}
		st.TopRoutes = append(st.TopRoutes, r)
	}
	return st, rows.Err()
}
