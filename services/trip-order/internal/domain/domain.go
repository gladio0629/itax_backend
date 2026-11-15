// Package domain — buyurtma, taklif, safar va baho turlari hamda holat qoidalari.
package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type OrderType string

const (
	TypePassenger OrderType = "passenger"
	TypeDelivery  OrderType = "delivery"
)

type CarClass string

const (
	Economy  CarClass = "economy"
	Comfort  CarClass = "comfort"
	Business CarClass = "business"
	Minivan  CarClass = "minivan"
)

type VehicleType string

const (
	Spark  VehicleType = "spark"
	Damas  VehicleType = "damas"
	Labo   VehicleType = "labo"
	Isuzu  VehicleType = "isuzu"
	Isuzu2 VehicleType = "isuzu2"
	Fura   VehicleType = "fura"
)

type PaymentMethod string

const (
	Cash   PaymentMethod = "cash"
	Card   PaymentMethod = "card"
	Wallet PaymentMethod = "wallet"
	Payme  PaymentMethod = "payme"
	Click  PaymentMethod = "click"
)

func (c CarClass) Valid() bool {
	switch c {
	case Economy, Comfort, Business, Minivan:
		return true
	}
	return false
}

func (v VehicleType) Valid() bool {
	switch v {
	case Spark, Damas, Labo, Isuzu, Isuzu2, Fura:
		return true
	}
	return false
}

func (p PaymentMethod) Valid() bool {
	switch p {
	case Cash, Card, Wallet, Payme, Click:
		return true
	}
	return false
}

// OrderStatus — buyurtma holati.
//
//	searching -> accepted -> in_progress -> completed
//	searching -> cancelled (yo'lovchi)  |  searching -> expired (haydovchi topilmadi)
//
// OpenAPI enum'ida `arrived` ham bor, lekin unga o'tkazuvchi endpoint yo'q — amalga oshirilmagan.
type OrderStatus string

const (
	Searching  OrderStatus = "searching"
	Accepted   OrderStatus = "accepted"
	InProgress OrderStatus = "in_progress"
	Completed  OrderStatus = "completed"
	Cancelled  OrderStatus = "cancelled"
	Expired    OrderStatus = "expired"
)

var orderTransitions = map[OrderStatus][]OrderStatus{
	Searching:  {Accepted, Cancelled, Expired},
	Accepted:   {InProgress},
	InProgress: {Completed},
}

func (s OrderStatus) CanTransitionTo(next OrderStatus) bool {
	for _, n := range orderTransitions[s] {
		if n == next {
			return true
		}
	}
	return false
}

// Active — yo'lovchining faol buyurtmalar limitiga kiradimi (F-24).
func (s OrderStatus) Active() bool { return s == Searching || s == Accepted || s == InProgress }

func (s OrderStatus) Terminal() bool { return s == Completed || s == Cancelled || s == Expired }

type TripStatus string

const (
	TripAssigned   TripStatus = "assigned"
	TripInProgress TripStatus = "in_progress"
	TripCompleted  TripStatus = "completed"
	TripCancelled  TripStatus = "cancelled"
)

var tripTransitions = map[TripStatus][]TripStatus{
	TripAssigned:   {TripInProgress},
	TripInProgress: {TripCompleted},
}

func (s TripStatus) CanTransitionTo(next TripStatus) bool {
	for _, n := range tripTransitions[s] {
		if n == next {
			return true
		}
	}
	return false
}

type Point struct {
	Lat     float64
	Lng     float64
	Address string
}

// SeatID — mashinadagi o'rindiq joyi (seat map). Narx o'rindiqlar SONIGA bog'liq; joy faqat tanlov sifatida saqlanadi.
type SeatID string

const (
	SeatFront     SeatID = "front"
	SeatBackLeft  SeatID = "back-left"
	SeatBackMid   SeatID = "back-mid"
	SeatBackRight SeatID = "back-right"
)

// AllSeats — seat map tartibi.
var AllSeats = []SeatID{SeatFront, SeatBackLeft, SeatBackMid, SeatBackRight}

func (s SeatID) Valid() bool {
	switch s {
	case SeatFront, SeatBackLeft, SeatBackMid, SeatBackRight:
		return true
	}
	return false
}

// PriceLine — narx tarkibining bir qatori (klient i18n kaliti + qiymatlar + summa tiyinda).
// Buyurtma/taklif qatorlarining yig'indisi doim narxga teng.
type PriceLine struct {
	Key    string         `json:"key"`
	Params map[string]any `json:"params,omitempty"`
	Amount int64          `json:"amount"`
}

// SumLines — qatorlar yig'indisi.
func SumLines(ls []PriceLine) int64 {
	var s int64
	for _, l := range ls {
		s += l.Amount
	}
	return s
}

// DefaultLines — narx tarkibi noma'lum bo'lganda (eski buyurtma, Pricing qatorsiz javob) bitta qator.
func DefaultLines(t OrderType, seats *int, price int64) []PriceLine {
	switch {
	case t == TypeDelivery:
		return []PriceLine{{Key: "quote.cargoBase", Amount: price}}
	case seats != nil:
		return []PriceLine{{Key: "quote.seatFare", Params: map[string]any{"count": *seats}, Amount: price}}
	default:
		return []PriceLine{{Key: "quote.carFare", Amount: price}}
	}
}

// Quote — narx taklifi. Buyurtma quote_id bilan yaratilsa, aynan shu narx yoziladi (qayta hisoblanmaydi).
type Quote struct {
	ID          uuid.UUID
	PassengerID uuid.UUID
	InputHash   string
	Price       int64
	Commission  int64
	DistanceM   int
	DurationS   int
	Lines       []PriceLine
	ExpiresAt   time.Time
	CreatedAt   time.Time
	UsedOrderID *uuid.UUID
}

// City — shaharlar katalogi yozuvi.
type City struct {
	ID     uuid.UUID
	Slug   string
	Name   string
	Region string
	Lat    float64
	Lng    float64
}

// Place — qidiruv natijasi (shahar yoki gazetteer'dagi manzil).
type Place struct {
	ID        uuid.UUID
	CityID    uuid.UUID
	Name      string
	Area      string
	Kind      string // city | landmark | station | bus_station | airport | market | district | address
	Lat, Lng  float64
	Entrance  *string
	DistanceM *int
}

// SavedPlace — foydalanuvchining saqlangan manzili.
type SavedPlace struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Name      string
	Area      string
	Lat, Lng  float64
	Details   map[string]string // entrance, floor, apartment, intercom, note
	CreatedAt time.Time
}

// PopularRoute — mashhur shaharlararo yo'nalish.
type PopularRoute struct {
	ID       uuid.UUID
	From, To City
}

type Order struct {
	ID          uuid.UUID
	PassengerID uuid.UUID
	Type        OrderType

	// passenger: Seats YOKI CarClass; delivery: VehicleType.
	Seats       *int
	CarClass    *CarClass
	VehicleType *VehicleType

	// SeatIDs — tanlangan o'rindiq joylari (bo'lsa, Seats = len(SeatIDs)).
	SeatIDs []SeatID

	From, To Point
	// FromCityID/ToCityID — eng yaqin shahar (server aniqlaydi; shaharlardan uzoq bo'lsa nil).
	FromCityID, ToCityID *uuid.UUID
	// Vaqt: ScheduledAt (aniq vaqt) YOKI ScheduledFrom..ScheduledTo (oraliq); hammasi nil — hozir ("haydovchi istagan").
	ScheduledAt   *time.Time
	ScheduledFrom *time.Time
	ScheduledTo   *time.Time
	PaymentMethod PaymentMethod
	Comment       *string

	QuoteID      *uuid.UUID
	Lines        []PriceLine
	CancelReason *string

	Status     OrderStatus
	Price      int64 // tiyin
	Commission int64 // tiyin
	DistanceM  int
	DurationS  int

	DriverID *uuid.UUID
	TripID   *uuid.UUID

	IdempotencyKey  *string
	SearchExpiresAt time.Time
	Version         int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CheckShape — tur bo'yicha maydonlar mosligini tekshiradi.
func (o *Order) CheckShape() error {
	switch o.Type {
	case TypePassenger:
		if o.VehicleType != nil {
			return errors.New("vehicle_type faqat delivery uchun")
		}
		if (o.Seats == nil) == (o.CarClass == nil) {
			return errors.New("seats yoki car_class (faqat bittasi) kerak")
		}
		if o.Seats != nil && (*o.Seats < 1 || *o.Seats > 4) {
			return errors.New("seats 1..4")
		}
		if o.SeatIDs != nil && (o.Seats == nil || *o.Seats != len(o.SeatIDs)) {
			return errors.New("seat_ids soni seats ga teng emas")
		}
		if o.CarClass != nil && !o.CarClass.Valid() {
			return errors.New("car_class noto'g'ri")
		}
	case TypeDelivery:
		if o.Seats != nil || o.CarClass != nil || o.SeatIDs != nil {
			return errors.New("seats/car_class delivery uchun emas")
		}
		if o.VehicleType == nil || !o.VehicleType.Valid() {
			return errors.New("vehicle_type kerak")
		}
	default:
		return fmt.Errorf("type noto'g'ri: %q", o.Type)
	}
	return nil
}

type OfferStatus string

const (
	OfferPending  OfferStatus = "pending"
	OfferAccepted OfferStatus = "accepted"
	OfferClosed   OfferStatus = "closed"
)

type Offer struct {
	ID         uuid.UUID
	OrderID    uuid.UUID
	DriverID   uuid.UUID
	PickupETAs int
	ExpiresAt  time.Time
	Status     OfferStatus
	CreatedAt  time.Time
}

// OfferView — haydovchi ko'radigan taklif: o'zi + buyurtma ma'lumoti.
type OfferView struct {
	Offer
	Order Order
}

type Trip struct {
	ID          uuid.UUID
	OrderID     uuid.UUID
	DriverID    uuid.UUID
	PassengerID uuid.UUID
	Status      TripStatus
	StartedAt   *time.Time
	CompletedAt *time.Time
	FinalPrice  *int64
	Commission  *int64
	DistanceM   *int
	DurationS   *int
	CreatedAt   time.Time
}

type ReasonKind string

const (
	ReasonPolite      ReasonKind = "polite"
	ReasonGoodTalk    ReasonKind = "good_talk"
	ReasonComfort     ReasonKind = "comfort"
	ReasonJustBecause ReasonKind = "just_because"
	ReasonCustom      ReasonKind = "custom"
)

func (k ReasonKind) Valid() bool {
	switch k {
	case ReasonPolite, ReasonGoodTalk, ReasonComfort, ReasonJustBecause, ReasonCustom:
		return true
	}
	return false
}

type Rating struct {
	OrderID     uuid.UUID
	PassengerID uuid.UUID
	DriverID    uuid.UUID
	Stars       int
	Kind        *ReasonKind
	CustomText  *string
	CreatedAt   time.Time
	UpdatedAt   *time.Time
}

// Saqlash/biznes qatlami xatolari.
var (
	ErrNotFound     = errors.New("domain: not found")
	ErrAlreadyTaken = errors.New("domain: order already taken")
	ErrAlreadyRated = errors.New("domain: already rated")
	// ErrLimitReached — faol buyurtmalar/safarlar limiti (F-24); bazada tekshiriladi (haqiqat manbai).
	ErrLimitReached = errors.New("domain: active limit reached")
	// ErrQuoteUsed — narx taklifi boshqa buyurtmada ishlatilgan.
	ErrQuoteUsed = errors.New("domain: quote already used")
)

// StateError — amal hozirgi holatda mumkin emas.
type StateError struct {
	Order OrderStatus
	Trip  TripStatus
}

func (e *StateError) Error() string {
	return fmt.Sprintf("domain: invalid state (order=%s trip=%s)", e.Order, e.Trip)
}

// ValidateRating — baho qoidalari: 1..5; 5 yulduzda sabab (kind) majburiy; kind=custom bo'lsa custom_text
// majburiy (1..200 belgi); boshqa hollarda sabab ham, matn ham bo'lmaydi. Natija: maydon -> xato kaliti.
func ValidateRating(stars int, kind *ReasonKind, customText *string) map[string]string {
	f := map[string]string{}
	if stars < 1 || stars > 5 {
		f["stars"] = "out_of_range"
	}
	if stars != 5 {
		if kind != nil || customText != nil {
			f["reason"] = "only_for_5_stars"
		}
		return f
	}
	if kind == nil {
		f["reason"] = "required"
		return f
	}
	if !kind.Valid() {
		f["reason.kind"] = "invalid_value"
		return f
	}
	if *kind == ReasonCustom {
		if customText == nil {
			f["reason.custom_text"] = "required"
		} else if n := len([]rune(*customText)); n < 1 || n > 200 {
			f["reason.custom_text"] = "invalid_length"
		}
	} else if customText != nil {
		f["reason.custom_text"] = "only_for_custom"
	}
	return f
}
