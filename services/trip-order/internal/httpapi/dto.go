package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/apperr"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/izgo/trip-order/internal/service"
)

const maxBody = 16 << 10

type validator interface{ validate() map[string]string }

// decode — qat'iy JSON (noma'lum maydon taqiqlangan, bitta qiymat) + validatsiya. Buzuq JSON ham 422.
func decode(w http.ResponseWriter, r *http.Request, dst validator) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return apperr.Validation(jsonErrFields(err))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return apperr.Validation(map[string]string{"body": "invalid_json"})
	}
	if f := dst.validate(); len(f) > 0 {
		return apperr.Validation(f)
	}
	return nil
}

// decodeOptional — tana bo'lmasligi ham mumkin (cancel).
func decodeOptional(w http.ResponseWriter, r *http.Request, dst validator) error {
	body := http.MaxBytesReader(w, r.Body, maxBody)
	raw, err := io.ReadAll(body)
	if err != nil {
		return apperr.Validation(map[string]string{"body": "invalid_json"})
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	r.Body = io.NopCloser(strings.NewReader(string(raw)))
	return decode(w, r, dst)
}

func jsonErrFields(err error) map[string]string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return map[string]string{te.Field: "invalid_type"}
	}
	if s := err.Error(); strings.HasPrefix(s, "json: unknown field ") {
		return map[string]string{strings.Trim(strings.TrimPrefix(s, "json: unknown field "), `"`): "unknown_field"}
	}
	return map[string]string{"body": "invalid_json"}
}

// ---- umumiy

type pointDTO struct {
	Lat     *float64 `json:"lat"`
	Lng     *float64 `json:"lng"`
	Address string   `json:"address"`
}

func (p *pointDTO) check(f map[string]string, name string) {
	if p == nil {
		f[name] = "required"
		return
	}
	if p.Lat == nil || *p.Lat < -90 || *p.Lat > 90 || math.IsNaN(*p.Lat) {
		f[name+".lat"] = "invalid_value"
	}
	if p.Lng == nil || *p.Lng < -180 || *p.Lng > 180 || math.IsNaN(*p.Lng) {
		f[name+".lng"] = "invalid_value"
	}
	if utf8.RuneCountInString(p.Address) > 300 {
		f[name+".address"] = "too_long"
	}
}

func (p *pointDTO) domain() domain.Point {
	return domain.Point{Lat: *p.Lat, Lng: *p.Lng, Address: p.Address}
}

type pointResp struct {
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Address string  `json:"address,omitempty"`
}

func newPointResp(p domain.Point) pointResp {
	return pointResp{Lat: p.Lat, Lng: p.Lng, Address: p.Address}
}

func parseTime(f map[string]string, name string, s *string) *time.Time {
	if s == nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		f[name] = "invalid_format"
		return nil
	}
	t = t.UTC()
	return &t
}

func checkComment(f map[string]string, c *string) {
	if c != nil && utf8.RuneCountInString(*c) > 200 {
		f["comment"] = "too_long"
	}
}

// ---- buyurtma yaratish

type createOrderDTO struct {
	Type          string    `json:"type"`
	Seats         *int      `json:"seats"`
	SeatIDs       *[]string `json:"seat_ids"`
	CarClass      *string   `json:"car_class"`
	VehicleType   *string   `json:"vehicle_type"`
	From          *pointDTO `json:"from"`
	To            *pointDTO `json:"to"`
	ScheduledAt   *string   `json:"scheduled_at"`
	ScheduledFrom *string   `json:"scheduled_from"`
	ScheduledTo   *string   `json:"scheduled_to"`
	PaymentMethod string    `json:"payment_method"`
	Comment       *string   `json:"comment"`
	QuoteID       *string   `json:"quote_id"`

	scheduled, schedFrom, schedTo *time.Time
	seatIDs                       []domain.SeatID
	quoteID                       *uuid.UUID
}

// checkSeatIDs — seat_ids: 1..4 ta, takrorlanmas, ma'lum joylar.
func checkSeatIDs(f map[string]string, raw *[]string) []domain.SeatID {
	if raw == nil {
		return nil
	}
	if len(*raw) < 1 || len(*raw) > 4 {
		f["seat_ids"] = "out_of_range"
		return nil
	}
	seen := map[string]bool{}
	out := make([]domain.SeatID, 0, len(*raw))
	for _, v := range *raw {
		switch {
		case !domain.SeatID(v).Valid():
			f["seat_ids"] = "invalid_value"
			return nil
		case seen[v]:
			f["seat_ids"] = "duplicate"
			return nil
		}
		seen[v] = true
		out = append(out, domain.SeatID(v))
	}
	return out
}

func (d *createOrderDTO) validate() map[string]string { return d.check(false) }

func (d *createOrderDTO) check(quote bool) map[string]string {
	f := map[string]string{}
	switch domain.OrderType(d.Type) {
	case domain.TypePassenger:
		if d.VehicleType != nil {
			f["vehicle_type"] = "only_for_delivery"
		}
		switch {
		case d.Seats == nil && d.SeatIDs == nil && d.CarClass == nil:
			f["seats"] = "seats_or_car_class_required"
		case (d.Seats != nil || d.SeatIDs != nil) && d.CarClass != nil:
			f["car_class"] = "conflicts_with_seats"
		}
	case domain.TypeDelivery:
		if d.Seats != nil {
			f["seats"] = "not_applicable_to_delivery"
		}
		if d.SeatIDs != nil {
			f["seat_ids"] = "not_applicable_to_delivery"
		}
		if d.CarClass != nil {
			f["car_class"] = "not_applicable_to_delivery"
		}
		if d.VehicleType == nil {
			f["vehicle_type"] = "required"
		}
	case "":
		f["type"] = "required"
	default:
		f["type"] = "invalid_value"
	}
	if d.Seats != nil && (*d.Seats < 1 || *d.Seats > 4) {
		f["seats"] = "out_of_range"
	}
	d.seatIDs = checkSeatIDs(f, d.SeatIDs)
	if d.seatIDs != nil && d.Seats != nil && *d.Seats != len(d.seatIDs) {
		f["seats"] = "mismatch_seat_ids"
	}
	if d.CarClass != nil && !domain.CarClass(*d.CarClass).Valid() {
		f["car_class"] = "invalid_value"
	}
	if d.VehicleType != nil && !domain.VehicleType(*d.VehicleType).Valid() {
		f["vehicle_type"] = "invalid_value"
	}
	d.From.check(f, "from")
	d.To.check(f, "to")
	switch {
	case d.PaymentMethod == "" && !quote:
		f["payment_method"] = "required"
	case d.PaymentMethod != "" && !domain.PaymentMethod(d.PaymentMethod).Valid():
		f["payment_method"] = "invalid_value"
	}
	d.scheduled = parseTime(f, "scheduled_at", d.ScheduledAt)
	d.schedFrom = parseTime(f, "scheduled_from", d.ScheduledFrom)
	d.schedTo = parseTime(f, "scheduled_to", d.ScheduledTo)
	if d.ScheduledAt != nil && (d.ScheduledFrom != nil || d.ScheduledTo != nil) {
		f["scheduled_at"] = "conflicts_with_range"
	}
	checkComment(f, d.Comment)
	if d.QuoteID != nil {
		if quote {
			f["quote_id"] = "unknown_field"
		} else if id, err := uuid.Parse(*d.QuoteID); err != nil {
			f["quote_id"] = "invalid_format"
		} else {
			d.quoteID = &id
		}
	}
	return f
}

func (d *createOrderDTO) input() service.CreateInput {
	in := service.CreateInput{
		Type: domain.OrderType(d.Type), Seats: d.Seats, SeatIDs: d.seatIDs, From: d.From.domain(), To: d.To.domain(),
		ScheduledAt: d.scheduled, ScheduledFrom: d.schedFrom, ScheduledTo: d.schedTo,
		PaymentMethod: domain.PaymentMethod(d.PaymentMethod), Comment: d.Comment, QuoteID: d.quoteID,
	}
	if d.seatIDs != nil {
		n := len(d.seatIDs)
		in.Seats = &n
	}
	if d.CarClass != nil {
		c := domain.CarClass(*d.CarClass)
		in.CarClass = &c
	}
	if d.VehicleType != nil {
		v := domain.VehicleType(*d.VehicleType)
		in.VehicleType = &v
	}
	return in
}

// quoteDTO — POST /orders/quote: buyurtma bilan bir xil maydonlar; payment_method ixtiyoriy, quote_id yo'q.
type quoteDTO struct{ createOrderDTO }

func (d *quoteDTO) validate() map[string]string { return d.check(true) }

// ---- buyurtmani o'zgartirish

type updateOrderDTO struct {
	Seats         *int      `json:"seats"`
	SeatIDs       *[]string `json:"seat_ids"`
	CarClass      *string   `json:"car_class"`
	VehicleType   *string   `json:"vehicle_type"`
	To            *pointDTO `json:"to"`
	ScheduledAt   *string   `json:"scheduled_at"`
	ScheduledFrom *string   `json:"scheduled_from"`
	ScheduledTo   *string   `json:"scheduled_to"`
	PaymentMethod *string   `json:"payment_method"`
	Comment       *string   `json:"comment"`

	scheduled, schedFrom, schedTo *time.Time
	seatIDs                       []domain.SeatID
}

func (d *updateOrderDTO) validate() map[string]string {
	f := map[string]string{}
	if d.Seats == nil && d.SeatIDs == nil && d.CarClass == nil && d.VehicleType == nil && d.To == nil && d.ScheduledAt == nil &&
		d.ScheduledFrom == nil && d.ScheduledTo == nil && d.PaymentMethod == nil && d.Comment == nil {
		f["body"] = "at_least_one_field"
		return f
	}
	if d.Seats != nil && (*d.Seats < 1 || *d.Seats > 4) {
		f["seats"] = "out_of_range"
	}
	d.seatIDs = checkSeatIDs(f, d.SeatIDs)
	if d.seatIDs != nil && d.Seats != nil && *d.Seats != len(d.seatIDs) {
		f["seats"] = "mismatch_seat_ids"
	}
	if d.CarClass != nil && !domain.CarClass(*d.CarClass).Valid() {
		f["car_class"] = "invalid_value"
	}
	if d.VehicleType != nil && !domain.VehicleType(*d.VehicleType).Valid() {
		f["vehicle_type"] = "invalid_value"
	}
	if d.To != nil {
		d.To.check(f, "to")
	}
	if d.PaymentMethod != nil && !domain.PaymentMethod(*d.PaymentMethod).Valid() {
		f["payment_method"] = "invalid_value"
	}
	d.scheduled = parseTime(f, "scheduled_at", d.ScheduledAt)
	d.schedFrom = parseTime(f, "scheduled_from", d.ScheduledFrom)
	d.schedTo = parseTime(f, "scheduled_to", d.ScheduledTo)
	if d.ScheduledAt != nil && (d.ScheduledFrom != nil || d.ScheduledTo != nil) {
		f["scheduled_at"] = "conflicts_with_range"
	}
	checkComment(f, d.Comment)
	return f
}

func (d *updateOrderDTO) input() service.UpdateInput {
	in := service.UpdateInput{Seats: d.Seats, SeatIDs: d.seatIDs, ScheduledAt: d.scheduled, ScheduledFrom: d.schedFrom,
		ScheduledTo: d.schedTo, Comment: d.Comment}
	if d.CarClass != nil {
		c := domain.CarClass(*d.CarClass)
		in.CarClass = &c
	}
	if d.VehicleType != nil {
		v := domain.VehicleType(*d.VehicleType)
		in.VehicleType = &v
	}
	if d.To != nil {
		p := d.To.domain()
		in.To = &p
	}
	if d.PaymentMethod != nil {
		m := domain.PaymentMethod(*d.PaymentMethod)
		in.PaymentMethod = &m
	}
	return in
}

// ---- bekor qilish

type cancelDTO struct {
	Reason  *string `json:"reason"`
	Comment *string `json:"comment"`
}

func (d *cancelDTO) validate() map[string]string {
	f := map[string]string{}
	if d.Reason != nil {
		switch *d.Reason {
		case "changed_plans", "long_wait", "driver_asked", "wrong_address", "other":
		default:
			f["reason"] = "invalid_value"
		}
	}
	checkComment(f, d.Comment)
	return f
}

// ---- baho

type ratingDTO struct {
	Stars  *int `json:"stars"`
	Reason *struct {
		Kind       *string `json:"kind"`
		CustomText *string `json:"custom_text"`
	} `json:"reason"`
}

func (d *ratingDTO) validate() map[string]string {
	if d.Stars == nil {
		return map[string]string{"stars": "required"}
	}
	in := d.input()
	f := domain.ValidateRating(in.Stars, in.Kind, in.CustomText)
	if d.Reason != nil && *d.Stars != 5 {
		f["reason"] = "only_for_5_stars"
	} else if d.Reason != nil && d.Reason.Kind == nil {
		f["reason.kind"] = "required"
	}
	return f
}

func (d *ratingDTO) input() service.RatingInput {
	in := service.RatingInput{Stars: *d.Stars}
	if d.Reason != nil {
		if d.Reason.Kind != nil {
			k := domain.ReasonKind(*d.Reason.Kind)
			in.Kind = &k
		}
		if d.Reason.CustomText != nil {
			t := strings.TrimSpace(*d.Reason.CustomText)
			in.CustomText = &t
		}
	}
	return in
}

// ---- javoblar

type driverResp struct {
	ID uuid.UUID `json:"id"`
}

type orderResp struct {
	ID            uuid.UUID          `json:"id"`
	Type          string             `json:"type"`
	Seats         *int               `json:"seats,omitempty"`
	SeatIDs       []string           `json:"seat_ids,omitempty"`
	CarClass      *string            `json:"car_class,omitempty"`
	VehicleType   *string            `json:"vehicle_type,omitempty"`
	From          pointResp          `json:"from"`
	To            pointResp          `json:"to"`
	FromCityID    *uuid.UUID         `json:"from_city_id"`
	ToCityID      *uuid.UUID         `json:"to_city_id"`
	ScheduledAt   *time.Time         `json:"scheduled_at"`
	ScheduledFrom *time.Time         `json:"scheduled_from"`
	ScheduledTo   *time.Time         `json:"scheduled_to"`
	PaymentMethod string             `json:"payment_method"`
	Comment       *string            `json:"comment"`
	Status        string             `json:"status"`
	Price         int64              `json:"price"`
	PriceLines    []domain.PriceLine `json:"price_lines"`
	QuoteID       *uuid.UUID         `json:"quote_id"`
	DistanceM     int                `json:"distance_m"`
	DurationS     int                `json:"duration_s"`
	Driver        *driverResp        `json:"driver"`
	TripID        *uuid.UUID         `json:"trip_id"`
	CancelReason  *string            `json:"cancel_reason"`
	// Server qarorlari: klient holatdan o'zi xulosa chiqarmaydi.
	Editable  bool      `json:"editable"`
	CanCancel bool      `json:"can_cancel"`
	CreatedAt time.Time `json:"created_at"`
}

func strp[T ~string](v *T) *string {
	if v == nil {
		return nil
	}
	s := string(*v)
	return &s
}

func seatStrings(ids []domain.SeatID) []string {
	if ids == nil {
		return nil
	}
	out := make([]string, len(ids))
	for i, v := range ids {
		out[i] = string(v)
	}
	return out
}

// orderLines — saqlangan tarkib (yig'indisi narxga teng bo'lsa), aks holda bitta qator.
func orderLines(o domain.Order) []domain.PriceLine {
	if len(o.Lines) > 0 && domain.SumLines(o.Lines) == o.Price {
		return o.Lines
	}
	return domain.DefaultLines(o.Type, o.Seats, o.Price)
}

func newOrderResp(o domain.Order) orderResp {
	r := orderResp{
		ID: o.ID, Type: string(o.Type), Seats: o.Seats, SeatIDs: seatStrings(o.SeatIDs), CarClass: strp(o.CarClass), VehicleType: strp(o.VehicleType),
		From: newPointResp(o.From), To: newPointResp(o.To), FromCityID: o.FromCityID, ToCityID: o.ToCityID,
		ScheduledAt: utc(o.ScheduledAt), ScheduledFrom: utc(o.ScheduledFrom), ScheduledTo: utc(o.ScheduledTo),
		PaymentMethod: string(o.PaymentMethod), Comment: o.Comment, Status: string(o.Status), Price: o.Price, PriceLines: orderLines(o),
		QuoteID: o.QuoteID, DistanceM: o.DistanceM, DurationS: o.DurationS, TripID: o.TripID, CancelReason: o.CancelReason,
		Editable: o.Status == domain.Searching, CanCancel: o.Status == domain.Searching, CreatedAt: o.CreatedAt,
	}
	if o.DriverID != nil {
		r.Driver = &driverResp{ID: *o.DriverID}
	}
	return r
}

type page[T any] struct {
	Data   []T     `json:"data"`
	Cursor *string `json:"cursor"`
}

type offerResp struct {
	ID            uuid.UUID  `json:"id"`
	OrderID       uuid.UUID  `json:"order_id"`
	Type          string     `json:"type"`
	Seats         *int       `json:"seats,omitempty"`
	SeatIDs       []string   `json:"seat_ids,omitempty"`
	CarClass      *string    `json:"car_class,omitempty"`
	VehicleType   *string    `json:"vehicle_type,omitempty"`
	From          pointResp  `json:"from"`
	To            pointResp  `json:"to"`
	FromCityID    *uuid.UUID `json:"from_city_id"`
	ToCityID      *uuid.UUID `json:"to_city_id"`
	ScheduledAt   *time.Time `json:"scheduled_at"`
	ScheduledFrom *time.Time `json:"scheduled_from"`
	ScheduledTo   *time.Time `json:"scheduled_to"`
	PaymentMethod string     `json:"payment_method"`
	Comment       *string    `json:"comment"`
	Price         int64      `json:"price"`
	Commission    int64      `json:"commission"`
	DistanceM     int        `json:"distance_m"`
	PickupETAs    int        `json:"pickup_eta_s"`
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
}

func newOfferResp(v domain.OfferView) offerResp {
	o := v.Order
	return offerResp{
		ID: v.Offer.ID, OrderID: o.ID, Type: string(o.Type), Seats: o.Seats, SeatIDs: seatStrings(o.SeatIDs), CarClass: strp(o.CarClass),
		VehicleType: strp(o.VehicleType), From: newPointResp(o.From), To: newPointResp(o.To), FromCityID: o.FromCityID, ToCityID: o.ToCityID,
		ScheduledAt: utc(o.ScheduledAt), ScheduledFrom: utc(o.ScheduledFrom), ScheduledTo: utc(o.ScheduledTo),
		PaymentMethod: string(o.PaymentMethod), Comment: o.Comment, Price: o.Price, Commission: o.Commission, DistanceM: o.DistanceM,
		PickupETAs: v.Offer.PickupETAs, CreatedAt: v.Offer.CreatedAt, ExpiresAt: v.Offer.ExpiresAt,
	}
}

type tripResp struct {
	ID           uuid.UUID  `json:"id"`
	OrderID      uuid.UUID  `json:"order_id"`
	DriverID     uuid.UUID  `json:"driver_id"`
	Status       string     `json:"status"`
	ArrivedAt    *time.Time `json:"arrived_at"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	CancelledAt  *time.Time `json:"cancelled_at"`
	CancelledBy  *string    `json:"cancelled_by"`
	FinalPrice   *int64     `json:"final_price"`
	Commission   *int64     `json:"commission"`
	DistanceM    *int       `json:"distance_m"`
	NextActions  []string   `json:"next_actions"`
	CancelReason *string    `json:"cancel_reason"`
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func newTripResp(t domain.Trip) tripResp {
	t.StartedAt, t.CompletedAt = utc(t.StartedAt), utc(t.CompletedAt)
	return tripResp{ID: t.ID, OrderID: t.OrderID, DriverID: t.DriverID, Status: string(t.Status), ArrivedAt: utc(t.ArrivedAt),
		StartedAt: t.StartedAt, CompletedAt: t.CompletedAt, CancelledAt: utc(t.CancelledAt), CancelledBy: t.CancelledBy,
		FinalPrice: t.FinalPrice, Commission: t.Commission, DistanceM: t.DistanceM, NextActions: t.NextActions(), CancelReason: t.CancelReason}
}

type ratingResp struct {
	OrderID   uuid.UUID   `json:"order_id"`
	Stars     int         `json:"stars"`
	Reason    *reasonResp `json:"reason"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt *time.Time  `json:"updated_at"`
}

type reasonResp struct {
	Kind       string  `json:"kind"`
	CustomText *string `json:"custom_text"`
}

func newRatingResp(r domain.Rating) ratingResp {
	out := ratingResp{OrderID: r.OrderID, Stars: r.Stars, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.Kind != nil {
		out.Reason = &reasonResp{Kind: string(*r.Kind), CustomText: r.CustomText}
	}
	return out
}
