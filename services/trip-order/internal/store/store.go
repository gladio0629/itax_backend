package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/izgo/trip-order/internal/events"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store — PostgreSQL. Barcha vaqtlar chaqiruvchidan beriladi (SQL'da now() ishlatilmaydi): testlarda soat boshqariladi.
type Store struct{ pool *pgxpool.Pool }

var _ events.Outbox = (*Store)(nil)

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// dbq — pool ham, tranzaksiya ham shu interfeysni bajaradi.
type dbq interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (s *Store) inTx(ctx context.Context, f func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// lockUser — foydalanuvchi bo'yicha tranzaksiya darajasidagi advisory lock: bir foydalanuvchining limitni
// tekshiruvchi tranzaksiyalari ketma-ket bajariladi (parallel so'rovlar limitdan oshib keta olmaydi).
func lockUser(ctx context.Context, tx pgx.Tx, kind string, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, kind+":"+id.String())
	return err
}

func trunc(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// ---------------------------------------------------------------- scan helpers

const orderColList = `id, passenger_id, type, seats, car_class, vehicle_type,
	from_lat, from_lng, from_address, to_lat, to_lng, to_address, scheduled_at, payment_method, comment,
	status, price, commission, distance_m, duration_s, driver_id, trip_id, idempotency_key,
	search_expires_at, version, created_at, updated_at,
	seat_ids, scheduled_from, scheduled_to, quote_id, price_lines, from_city_id, to_city_id, cancel_reason`

// orderCols — alias bilan (JOIN uchun): o.id, o.passenger_id ...
func orderCols(alias string) string {
	if alias == "" {
		return orderColList
	}
	out := ""
	for i, c := range splitCols(orderColList) {
		if i > 0 {
			out += ", "
		}
		out += alias + "." + c
	}
	return out
}

func splitCols(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		switch r {
		case ',':
			out = append(out, cur)
			cur = ""
		case ' ', '\n', '\t':
		default:
			cur += string(r)
		}
	}
	return append(out, cur)
}

// orderScan — orderColList tartibidagi ustunlarni domain.Order ga o'qish (scanOrder va scanOfferView uchun umumiy).
type orderScan struct {
	typ, pay, status      string
	seats                 *int16
	carClass, vehicleType *string
	fromAddr, toAddr      *string
	seatIDs               []string
	lines                 []byte
}

func (sc *orderScan) dest(o *domain.Order) []any {
	return []any{&o.ID, &o.PassengerID, &sc.typ, &sc.seats, &sc.carClass, &sc.vehicleType,
		&o.From.Lat, &o.From.Lng, &sc.fromAddr, &o.To.Lat, &o.To.Lng, &sc.toAddr, &o.ScheduledAt, &sc.pay, &o.Comment,
		&sc.status, &o.Price, &o.Commission, &o.DistanceM, &o.DurationS, &o.DriverID, &o.TripID, &o.IdempotencyKey,
		&o.SearchExpiresAt, &o.Version, &o.CreatedAt, &o.UpdatedAt,
		&sc.seatIDs, &o.ScheduledFrom, &o.ScheduledTo, &o.QuoteID, &sc.lines, &o.FromCityID, &o.ToCityID, &o.CancelReason}
}

func (sc *orderScan) finish(o *domain.Order) error {
	o.Type, o.PaymentMethod, o.Status = domain.OrderType(sc.typ), domain.PaymentMethod(sc.pay), domain.OrderStatus(sc.status)
	if sc.seats != nil {
		v := int(*sc.seats)
		o.Seats = &v
	}
	if sc.carClass != nil {
		v := domain.CarClass(*sc.carClass)
		o.CarClass = &v
	}
	if sc.vehicleType != nil {
		v := domain.VehicleType(*sc.vehicleType)
		o.VehicleType = &v
	}
	if sc.fromAddr != nil {
		o.From.Address = *sc.fromAddr
	}
	if sc.toAddr != nil {
		o.To.Address = *sc.toAddr
	}
	if sc.seatIDs != nil {
		o.SeatIDs = make([]domain.SeatID, len(sc.seatIDs))
		for i, v := range sc.seatIDs {
			o.SeatIDs[i] = domain.SeatID(v)
		}
	}
	if len(sc.lines) > 0 {
		if err := json.Unmarshal(sc.lines, &o.Lines); err != nil {
			return fmt.Errorf("price_lines: %w", err)
		}
	}
	o.CreatedAt, o.UpdatedAt, o.SearchExpiresAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC(), o.SearchExpiresAt.UTC()
	o.ScheduledAt, o.ScheduledFrom, o.ScheduledTo = utcp(o.ScheduledAt), utcp(o.ScheduledFrom), utcp(o.ScheduledTo)
	return nil
}

func utcp(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func scanOrder(row pgx.Row) (domain.Order, error) {
	var o domain.Order
	var sc orderScan
	if err := row.Scan(sc.dest(&o)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Order{}, domain.ErrNotFound
		}
		return domain.Order{}, err
	}
	if err := sc.finish(&o); err != nil {
		return domain.Order{}, err
	}
	return o, nil
}

// seatArg — text[] ustuni uchun (nil -> NULL).
func seatArg(ids []domain.SeatID) []string {
	if ids == nil {
		return nil
	}
	out := make([]string, len(ids))
	for i, v := range ids {
		out[i] = string(v)
	}
	return out
}

// linesArg — jsonb ustuni uchun (bo'sh -> NULL).
func linesArg(ls []domain.PriceLine) ([]byte, error) {
	if len(ls) == 0 {
		return nil, nil
	}
	return json.Marshal(ls)
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

const tripCols = `id, order_id, driver_id, passenger_id, status, started_at, completed_at, final_price, commission, distance_m, duration_s, created_at`

func scanTrip(row pgx.Row) (domain.Trip, error) {
	var t domain.Trip
	var st string
	if err := row.Scan(&t.ID, &t.OrderID, &t.DriverID, &t.PassengerID, &st, &t.StartedAt, &t.CompletedAt,
		&t.FinalPrice, &t.Commission, &t.DistanceM, &t.DurationS, &t.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Trip{}, domain.ErrNotFound
		}
		return domain.Trip{}, err
	}
	t.Status = domain.TripStatus(st)
	t.CreatedAt = t.CreatedAt.UTC()
	return t, nil
}

func insertOutbox(ctx context.Context, q dbq, e events.Event) error {
	_, err := q.Exec(ctx, `INSERT INTO events_outbox (id, subject, payload, created_at) VALUES ($1, $2, $3, $4)`,
		e.ID, e.Subject, e.Payload, trunc(e.CreatedAt))
	if err != nil {
		return fmt.Errorf("outbox: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------- orders

// CreateOrder — buyurtma + takliflar + order.created hodisasi bitta tranzaksiyada.
// replay=true: shu (passenger, Idempotency-Key) bilan buyurtma allaqachon bor — mavjudi qaytadi, hech narsa yozilmaydi.
// F-24: yo'lovchining faol buyurtmalari activeLimit ga yetgan bo'lsa ErrLimitReached (advisory lock ostida sanaladi).
func (s *Store) CreateOrder(ctx context.Context, o domain.Order, offers []domain.Offer, ev events.Event, activeLimit int) (domain.Order, bool, error) {
	var out domain.Order
	replay := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockUser(ctx, tx, "p", o.PassengerID); err != nil {
			return err
		}
		if o.IdempotencyKey != nil {
			ex, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderColList+` FROM orders WHERE passenger_id = $1 AND idempotency_key = $2`,
				o.PassengerID, *o.IdempotencyKey))
			if err == nil {
				replay, out = true, ex
				return nil
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return err
			}
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM orders WHERE passenger_id=$1 AND status IN ('searching','accepted','in_progress')`,
			o.PassengerID).Scan(&active); err != nil {
			return err
		}
		if active >= activeLimit {
			return domain.ErrLimitReached
		}
		lines, err := linesArg(o.Lines)
		if err != nil {
			return err
		}
		var inserted uuid.UUID
		err = tx.QueryRow(ctx, `
			INSERT INTO orders (id, passenger_id, type, seats, car_class, vehicle_type,
				from_lat, from_lng, from_address, to_lat, to_lng, to_address, scheduled_at, payment_method, comment,
				status, price, commission, distance_m, duration_s, idempotency_key, search_expires_at, version, created_at, updated_at,
				seat_ids, scheduled_from, scheduled_to, quote_id, price_lines, from_city_id, to_city_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,1,$23,$23,$24,$25,$26,$27,$28,$29,$30)
			ON CONFLICT (passenger_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
			RETURNING id`,
			o.ID, o.PassengerID, string(o.Type), o.Seats, o.CarClass, o.VehicleType,
			o.From.Lat, o.From.Lng, nilIfEmpty(o.From.Address), o.To.Lat, o.To.Lng, nilIfEmpty(o.To.Address),
			o.ScheduledAt, string(o.PaymentMethod), o.Comment, string(o.Status), o.Price, o.Commission, o.DistanceM, o.DurationS,
			o.IdempotencyKey, trunc(o.SearchExpiresAt), trunc(o.CreatedAt),
			seatArg(o.SeatIDs), o.ScheduledFrom, o.ScheduledTo, o.QuoteID, lines, o.FromCityID, o.ToCityID).Scan(&inserted)
		if errors.Is(err, pgx.ErrNoRows) {
			replay = true
			out, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderColList+` FROM orders WHERE passenger_id = $1 AND idempotency_key = $2`,
				o.PassengerID, o.IdempotencyKey))
			return err
		}
		if err != nil {
			return err
		}
		// Narx taklifi bir marta ishlatiladi: boshqa buyurtma olgan bo'lsa tranzaksiya bekor (ErrQuoteUsed).
		if o.QuoteID != nil {
			tag, err := tx.Exec(ctx, `UPDATE quotes SET used_order_id=$1 WHERE id=$2 AND used_order_id IS NULL`, o.ID, *o.QuoteID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return domain.ErrQuoteUsed
			}
		}
		for _, of := range offers {
			if _, err := tx.Exec(ctx, `INSERT INTO offers (id, order_id, driver_id, pickup_eta_s, status, expires_at, created_at)
				VALUES ($1,$2,$3,$4,'pending',$5,$6) ON CONFLICT (order_id, driver_id) DO NOTHING`,
				of.ID, of.OrderID, of.DriverID, of.PickupETAs, trunc(of.ExpiresAt), trunc(of.CreatedAt)); err != nil {
				return fmt.Errorf("offer: %w", err)
			}
		}
		if err := insertOutbox(ctx, tx, ev); err != nil {
			return err
		}
		out, err = scanOrder(tx.QueryRow(ctx, `SELECT `+orderColList+` FROM orders WHERE id = $1`, o.ID))
		return err
	})
	if err != nil {
		return domain.Order{}, false, err
	}
	return out, replay, nil
}

func (s *Store) GetOrder(ctx context.Context, id uuid.UUID) (domain.Order, error) {
	return scanOrder(s.pool.QueryRow(ctx, `SELECT `+orderColList+` FROM orders WHERE id = $1`, id))
}

func (s *Store) GetOrderByIdempotency(ctx context.Context, passenger uuid.UUID, key string) (domain.Order, error) {
	return scanOrder(s.pool.QueryRow(ctx, `SELECT `+orderColList+` FROM orders WHERE passenger_id = $1 AND idempotency_key = $2`, passenger, key))
}

// ListOrders — yo'lovchi tarixi (yangidan eskiga). limit+1 tagacha qaytaradi: oxirgisi "keyingi sahifa bor" belgisi.
func (s *Store) ListOrders(ctx context.Context, passenger uuid.UUID, cur *Cursor, limit int) ([]domain.Order, error) {
	var at *time.Time
	var id *uuid.UUID
	if cur != nil {
		at, id = &cur.At, &cur.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+orderColList+` FROM orders
		WHERE passenger_id = $1 AND ($2::timestamptz IS NULL OR (created_at, id) < ($2::timestamptz, $3::uuid))
		ORDER BY created_at DESC, id DESC LIMIT $4`, passenger, at, id, limit+1)
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

// UpdateSearching — parametrlarni optimistik (version) yangilaydi. false: buyurtma endi searching emas yoki versiya o'zgargan.
func (s *Store) UpdateSearching(ctx context.Context, o domain.Order, expectedVersion int, now time.Time) (bool, error) {
	updated := false
	lines, err := linesArg(o.Lines)
	if err != nil {
		return false, err
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE orders SET seats=$4, car_class=$5, vehicle_type=$6, to_lat=$7, to_lng=$8, to_address=$9,
				scheduled_at=$10, payment_method=$11, comment=$12, price=$13, commission=$14, distance_m=$15, duration_s=$16,
				search_expires_at=$17, version=version+1, updated_at=$18,
				seat_ids=$19, scheduled_from=$20, scheduled_to=$21, price_lines=$22, to_city_id=$23
			WHERE id=$1 AND passenger_id=$2 AND status='searching' AND version=$3`,
			o.ID, o.PassengerID, expectedVersion, o.Seats, o.CarClass, o.VehicleType, o.To.Lat, o.To.Lng, nilIfEmpty(o.To.Address),
			o.ScheduledAt, string(o.PaymentMethod), o.Comment, o.Price, o.Commission, o.DistanceM, o.DurationS,
			trunc(o.SearchExpiresAt), trunc(now), seatArg(o.SeatIDs), o.ScheduledFrom, o.ScheduledTo, lines, o.ToCityID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		updated = true
		_, err = tx.Exec(ctx, `UPDATE offers SET expires_at=$2 WHERE order_id=$1 AND status='pending'`, o.ID, trunc(o.SearchExpiresAt))
		return err
	})
	return updated, err
}

// CancelOrder: searching -> cancelled. changed=false: allaqachon bekor qilingan edi (takroriy chaqiruv).
// Boshqa holatlarda *domain.StateError; boshqa yo'lovchining buyurtmasi uchun ErrNotFound.
func (s *Store) CancelOrder(ctx context.Context, id, passenger uuid.UUID, reason, comment *string, now time.Time) (domain.Order, bool, error) {
	var out domain.Order
	changed := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		o, err := scanOrder(tx.QueryRow(ctx, `
			UPDATE orders SET status='cancelled', cancel_reason=$3, cancel_comment=$4, version=version+1, updated_at=$5
			WHERE id=$1 AND passenger_id=$2 AND status='searching' RETURNING `+orderColList,
			id, passenger, reason, comment, trunc(now)))
		if err == nil {
			changed, out = true, o
			_, err = tx.Exec(ctx, `UPDATE offers SET status='closed' WHERE order_id=$1 AND status='pending'`, id)
			return err
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		cur, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderColList+` FROM orders WHERE id=$1 AND passenger_id=$2`, id, passenger))
		if err != nil {
			return err
		}
		if cur.Status == domain.Cancelled {
			out = cur
			return nil
		}
		return &domain.StateError{Order: cur.Status}
	})
	return out, changed, err
}

// ExpireStale: qidiruv muddati o'tgan searching buyurtmalarni expired qiladi.
func (s *Store) ExpireStale(ctx context.Context, now time.Time) ([]domain.Order, error) {
	var out []domain.Order
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			UPDATE orders SET status='expired', version=version+1, updated_at=$1
			WHERE status='searching' AND search_expires_at <= $1 RETURNING `+orderColList, trunc(now))
		if err != nil {
			return err
		}
		for rows.Next() {
			o, err := scanOrder(rows)
			if err != nil {
				rows.Close()
				return err
			}
			out = append(out, o)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(out) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(out))
		for i, o := range out {
			ids[i] = o.ID
		}
		_, err = tx.Exec(ctx, `UPDATE offers SET status='closed' WHERE order_id = ANY($1) AND status='pending'`, ids)
		return err
	})
	return out, err
}

// CountActiveOrders — yo'lovchining faol buyurtmalari soni (Redis hisoblagichini tuzatish uchun).
func (s *Store) CountActiveOrders(ctx context.Context, passenger uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE passenger_id=$1 AND status IN ('searching','accepted','in_progress')`, passenger).Scan(&n)
	return n, err
}

// CountActiveTrips — haydovchining faol safarlari soni.
func (s *Store) CountActiveTrips(ctx context.Context, driver uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM trips WHERE driver_id=$1 AND status IN ('assigned','in_progress')`, driver).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------- offers

func scanOfferView(row pgx.Row) (domain.OfferView, error) {
	var v domain.OfferView
	var st string
	var sc orderScan
	dest := append([]any{&v.Offer.ID, &v.Offer.OrderID, &v.Offer.DriverID, &v.Offer.PickupETAs, &st, &v.Offer.ExpiresAt, &v.Offer.CreatedAt},
		sc.dest(&v.Order)...)
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OfferView{}, domain.ErrNotFound
		}
		return domain.OfferView{}, err
	}
	v.Offer.Status = domain.OfferStatus(st)
	v.Offer.ExpiresAt, v.Offer.CreatedAt = v.Offer.ExpiresAt.UTC(), v.Offer.CreatedAt.UTC()
	if err := sc.finish(&v.Order); err != nil {
		return domain.OfferView{}, err
	}
	return v, nil
}

const offerJoin = `
	SELECT f.id, f.order_id, f.driver_id, f.pickup_eta_s, f.status, f.expires_at, f.created_at, ` + "%s" + `
	FROM offers f JOIN orders o ON o.id = f.order_id `

func offerSelect() string { return fmt.Sprintf(offerJoin, orderCols("o")) }

// ListOffers — faqat shu haydovchining, hali amal qiladigan (pending, muddati o'tmagan, buyurtma searching) takliflari.
func (s *Store) ListOffers(ctx context.Context, driver uuid.UUID, now time.Time, cur *Cursor, limit int) ([]domain.OfferView, error) {
	var at *time.Time
	var id *uuid.UUID
	if cur != nil {
		at, id = &cur.At, &cur.ID
	}
	rows, err := s.pool.Query(ctx, offerSelect()+`
		WHERE f.driver_id=$1 AND f.status='pending' AND f.expires_at > $2 AND o.status='searching'
		  AND ($3::timestamptz IS NULL OR (f.created_at, f.id) < ($3::timestamptz, $4::uuid))
		ORDER BY f.created_at DESC, f.id DESC LIMIT $5`, driver, trunc(now), at, id, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OfferView
	for rows.Next() {
		v, err := scanOfferView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetOffer — taklif faqat o'z haydovchisiga ko'rinadi.
func (s *Store) GetOffer(ctx context.Context, offerID, driver uuid.UUID) (domain.OfferView, error) {
	return scanOfferView(s.pool.QueryRow(ctx, offerSelect()+`WHERE f.id=$1 AND f.driver_id=$2`, offerID, driver))
}

// AcceptOffer — atomar qabul: buyurtma searching bo'lsagina accepted bo'ladi (UPDATE ... WHERE status='searching'),
// safar yaratiladi, qolgan takliflar yopiladi, order.accepted hodisasi outbox'ga yoziladi.
// Redis qulfi (SETNX) bundan oldin keladi; ikkalasi birgalikda "birinchi qabul qilgan oladi"ni kafolatlaydi.
//
// Xatolar: ErrNotFound (taklif yo'q/begona/muddati o'tgan/buyurtma bekor yoki expired), ErrAlreadyTaken.
func (s *Store) AcceptOffer(ctx context.Context, offerID, driver uuid.UUID, now time.Time, activeLimit int,
	mkEvent func(orderID, tripID uuid.UUID) (events.Event, error)) (domain.Trip, error) {
	var trip domain.Trip
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var orderID uuid.UUID
		var offerStatus string
		var expires time.Time
		err := tx.QueryRow(ctx, `SELECT order_id, status, expires_at FROM offers WHERE id=$1 AND driver_id=$2`, offerID, driver).
			Scan(&orderID, &offerStatus, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if offerStatus == string(domain.OfferAccepted) { // qayta urinish: xuddi shu haydovchi allaqachon olgan
			trip, err = scanTrip(tx.QueryRow(ctx, `SELECT `+tripCols+` FROM trips WHERE order_id=$1 AND driver_id=$2`, orderID, driver))
			return err
		}
		if !expires.After(now) {
			return domain.ErrNotFound
		}

		// F-24: haydovchining faol safarlari limiti — haydovchi bo'yicha lock ostida (turli buyurtmalarni parallel
		// qabul qilish ham limitdan oshira olmaydi; per-order Redis qulfi buni qamrab olmaydi).
		if err := lockUser(ctx, tx, "d", driver); err != nil {
			return err
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM trips WHERE driver_id=$1 AND status IN ('assigned','in_progress')`, driver).Scan(&active); err != nil {
			return err
		}
		if active >= activeLimit {
			return domain.ErrLimitReached
		}

		tripID, err := uuid.NewV7()
		if err != nil {
			return err
		}
		var passenger uuid.UUID
		err = tx.QueryRow(ctx, `
			UPDATE orders SET status='accepted', driver_id=$2, trip_id=$3, version=version+1, updated_at=$4
			WHERE id=$1 AND status='searching' RETURNING passenger_id`, orderID, driver, tripID, trunc(now)).Scan(&passenger)
		if errors.Is(err, pgx.ErrNoRows) {
			var st string
			if e := tx.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1`, orderID).Scan(&st); e != nil {
				return e
			}
			switch domain.OrderStatus(st) {
			case domain.Accepted, domain.InProgress, domain.Completed:
				return domain.ErrAlreadyTaken
			default: // cancelled / expired
				return domain.ErrNotFound
			}
		}
		if err != nil {
			return err
		}
		trip, err = scanTrip(tx.QueryRow(ctx, `
			INSERT INTO trips (id, order_id, driver_id, passenger_id, status, created_at)
			VALUES ($1,$2,$3,$4,'assigned',$5) RETURNING `+tripCols, tripID, orderID, driver, passenger, trunc(now)))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE offers SET status = CASE WHEN id=$2 THEN 'accepted' ELSE 'closed' END
			WHERE order_id=$1 AND status='pending'`, orderID, offerID); err != nil {
			return err
		}
		ev, err := mkEvent(orderID, tripID)
		if err != nil {
			return err
		}
		return insertOutbox(ctx, tx, ev)
	})
	return trip, err
}

// ---------------------------------------------------------------- trips

// GetTrip — safar faqat o'z haydovchisiga ko'rinadi (F-10).
func (s *Store) GetTrip(ctx context.Context, tripID, driver uuid.UUID) (domain.Trip, error) {
	return scanTrip(s.pool.QueryRow(ctx, `SELECT `+tripCols+` FROM trips WHERE id=$1 AND driver_id=$2`, tripID, driver))
}

// StartTrip: assigned -> in_progress (buyurtma ham accepted -> in_progress).
func (s *Store) StartTrip(ctx context.Context, tripID, driver uuid.UUID, now time.Time) (domain.Trip, error) {
	var out domain.Trip
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		t, err := scanTrip(tx.QueryRow(ctx, `
			UPDATE trips SET status='in_progress', started_at=$3 WHERE id=$1 AND driver_id=$2 AND status='assigned'
			RETURNING `+tripCols, tripID, driver, trunc(now)))
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
		tag, err := tx.Exec(ctx, `UPDATE orders SET status='in_progress', version=version+1, updated_at=$2 WHERE id=$1 AND status='accepted'`,
			t.OrderID, trunc(now))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return &domain.StateError{Trip: t.Status}
		}
		out = t
		return nil
	})
	return out, err
}

// GetTripWithOrder — yakunlash oldidan yakuniy narxni hisoblash uchun.
func (s *Store) GetTripWithOrder(ctx context.Context, tripID, driver uuid.UUID) (domain.Trip, domain.Order, error) {
	t, err := s.GetTrip(ctx, tripID, driver)
	if err != nil {
		return domain.Trip{}, domain.Order{}, err
	}
	o, err := s.GetOrder(ctx, t.OrderID)
	return t, o, err
}

// FinalTrip — yakunlash natijasi (server hisoblagan).
type FinalTrip struct {
	Price      int64
	Commission int64
	DistanceM  int
}

// CompleteTrip: in_progress -> completed; buyurtma ham completed (narx yakuniysiga yangilanadi); trip.completed outbox'ga.
func (s *Store) CompleteTrip(ctx context.Context, tripID, driver uuid.UUID, fin FinalTrip, now time.Time,
	mkEvent func(domain.Trip) (events.Event, error)) (domain.Trip, error) {
	var out domain.Trip
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		t, err := scanTrip(tx.QueryRow(ctx, `
			UPDATE trips SET status='completed', completed_at=$3, final_price=$4, commission=$5, distance_m=$6,
				duration_s = GREATEST(0, floor(extract(epoch FROM ($3::timestamptz - started_at)))::int)
			WHERE id=$1 AND driver_id=$2 AND status='in_progress' RETURNING `+tripCols,
			tripID, driver, trunc(now), fin.Price, fin.Commission, fin.DistanceM))
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
		// narx o'zgargan bo'lsa eski tarkib noto'g'ri: NULL (javobda bitta qator chiqadi)
		tag, err := tx.Exec(ctx, `UPDATE orders SET status='completed', price_lines = CASE WHEN price=$2 THEN price_lines END,
				price=$2, commission=$3, distance_m=$4, version=version+1, updated_at=$5
			WHERE id=$1 AND status='in_progress'`, t.OrderID, fin.Price, fin.Commission, fin.DistanceM, trunc(now))
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
		out = t
		return nil
	})
	return out, err
}

// ---------------------------------------------------------------- ratings

func scanRating(row pgx.Row) (domain.Rating, error) {
	var r domain.Rating
	var kind *string
	var stars int16
	if err := row.Scan(&r.OrderID, &r.PassengerID, &r.DriverID, &stars, &kind, &r.CustomText, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Rating{}, domain.ErrNotFound
		}
		return domain.Rating{}, err
	}
	r.Stars = int(stars)
	if kind != nil {
		k := domain.ReasonKind(*kind)
		r.Kind = &k
	}
	r.CreatedAt = r.CreatedAt.UTC()
	if r.UpdatedAt != nil {
		u := r.UpdatedAt.UTC()
		r.UpdatedAt = &u
	}
	return r, nil
}

const ratingCols = `order_id, passenger_id, driver_id, stars, reason_kind, reason_custom_text, created_at, updated_at`

func (s *Store) GetRating(ctx context.Context, orderID uuid.UUID) (domain.Rating, error) {
	return scanRating(s.pool.QueryRow(ctx, `SELECT `+ratingCols+` FROM ratings WHERE order_id=$1`, orderID))
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// CreateRating — bitta buyurtmaga bitta baho; ikkinchisi ErrAlreadyRated.
func (s *Store) CreateRating(ctx context.Context, r domain.Rating) (domain.Rating, error) {
	out, err := scanRating(s.pool.QueryRow(ctx, `
		INSERT INTO ratings (order_id, passenger_id, driver_id, stars, reason_kind, reason_custom_text, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+ratingCols,
		r.OrderID, r.PassengerID, r.DriverID, r.Stars, r.Kind, r.CustomText, trunc(r.CreatedAt)))
	if isUnique(err) {
		return domain.Rating{}, domain.ErrAlreadyRated
	}
	return out, err
}

func (s *Store) UpdateRating(ctx context.Context, r domain.Rating, now time.Time) (domain.Rating, error) {
	return scanRating(s.pool.QueryRow(ctx, `
		UPDATE ratings SET stars=$2, reason_kind=$3, reason_custom_text=$4, updated_at=$5
		WHERE order_id=$1 RETURNING `+ratingCols, r.OrderID, r.Stars, r.Kind, r.CustomText, trunc(now)))
}

// ---------------------------------------------------------------- outbox

// DrainOutbox — yuborilmagan hodisalarni (SKIP LOCKED: replikalar bir-biriga xalaqit bermaydi) ketma-ket publish qiladi.
func (s *Store) DrainOutbox(ctx context.Context, limit int, publish func(context.Context, events.Event) error) (int, error) {
	sent := 0
	var pubErr error
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, subject, payload, created_at FROM events_outbox
			WHERE published_at IS NULL ORDER BY created_at, id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
		if err != nil {
			return err
		}
		var batch []events.Event
		for rows.Next() {
			var e events.Event
			if err := rows.Scan(&e.ID, &e.Subject, &e.Payload, &e.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, e := range batch {
			if pubErr = publish(ctx, e); pubErr != nil {
				break
			}
			if _, err := tx.Exec(ctx, `UPDATE events_outbox SET published_at=now() WHERE id=$1`, e.ID); err != nil {
				return err
			}
			sent++
		}
		return nil // yuborilganlar publish xatosida ham commit qilinadi
	})
	if err != nil {
		return 0, err
	}
	return sent, pubErr
}

// PurgeOutbox — yuborilganiga 7 kundan oshgan outbox yozuvlarini o'chiradi.
func (s *Store) PurgeOutbox(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM events_outbox WHERE published_at IS NOT NULL AND published_at < $1`, now.Add(-7*24*time.Hour))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
