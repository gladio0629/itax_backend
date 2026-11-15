package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/izgo/shared/go/authn"
	"github.com/izgo/trip-order/internal/apperr"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/izgo/trip-order/internal/service"
	"github.com/izgo/trip-order/internal/store"
)

// ---- haydovchi: rad etish, yetib keldim, bekor qilish, ro'yxatlar

type rejectDTO struct {
	Reason *string `json:"reason"`
}

func (d *rejectDTO) validate() map[string]string {
	if d.Reason == nil {
		return nil
	}
	switch *d.Reason {
	case "too_far", "bad_price", "busy", "other":
		return nil
	}
	return map[string]string{"reason": "invalid_value"}
}

func (s *Server) rejectOffer(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	oid, err := pathID(r)
	if err != nil {
		return err
	}
	var in rejectDTO
	if err := decodeOptional(w, r, &in); err != nil {
		return err
	}
	if err := s.svc.RejectOffer(r.Context(), id.UserID, oid, in.Reason); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) arriveTrip(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	tid, err := pathID(r)
	if err != nil {
		return err
	}
	t, err := s.svc.ArriveTrip(r.Context(), id.UserID, tid, requestID(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newTripResp(t))
	return nil
}

type tripCancelDTO struct {
	Reason  string  `json:"reason"`
	Comment *string `json:"comment"`
}

func (d *tripCancelDTO) validate() map[string]string {
	f := map[string]string{}
	switch {
	case d.Reason == "":
		f["reason"] = "required"
	case !service.DriverCancelReasons[d.Reason]:
		f["reason"] = "invalid_value"
	}
	checkComment(f, d.Comment)
	return f
}

func (s *Server) cancelTrip(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	tid, err := pathID(r)
	if err != nil {
		return err
	}
	var in tripCancelDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	t, err := s.svc.CancelTrip(r.Context(), id.UserID, tid, in.Reason, in.Comment, requestID(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newTripResp(t))
	return nil
}

type tripWithOrderResp struct {
	tripResp
	Order orderResp `json:"order"`
}

func (s *Server) listTrips(w http.ResponseWriter, r *http.Request, id *authn.Identity, statuses []string) error {
	cursor, limit, err := pageParams(r)
	if err != nil {
		return err
	}
	p, err := s.svc.Trips(r.Context(), id.UserID, statuses, cursor, limit)
	if err != nil {
		return err
	}
	out := page[tripWithOrderResp]{Data: make([]tripWithOrderResp, 0, len(p.Items)), Cursor: p.Cursor}
	for _, v := range p.Items {
		out.Data = append(out.Data, tripWithOrderResp{tripResp: newTripResp(v.Trip), Order: newOrderResp(v.Order)})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// tripsList — GET /api/v1/trips?status=assigned,in_progress (default: faol safarlar).
func (s *Server) tripsList(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	statuses := service.TripStatusesActive
	if v := r.URL.Query().Get("status"); v != "" {
		statuses = nil
		seen := map[string]bool{}
		for _, st := range strings.Split(v, ",") {
			st = strings.TrimSpace(st)
			switch domain.TripStatus(st) {
			case domain.TripAssigned, domain.TripInProgress, domain.TripCompleted, domain.TripCancelled:
			default:
				return apperr.Validation(map[string]string{"status": "invalid_value"})
			}
			if !seen[st] {
				seen[st] = true
				statuses = append(statuses, st)
			}
		}
	}
	return s.listTrips(w, r, id, statuses)
}

func (s *Server) tripsHistory(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	return s.listTrips(w, r, id, service.TripStatusesHistory)
}

// ---- ichki API (backoffice)

// internal — X-Internal-Token (INTERNAL_TOKENS dan biri, doimiy vaqtda solishtiriladi). Token sozlanmagan bo'lsa yopiq.
func (s *Server) internal(h handlerFunc) http.HandlerFunc {
	return s.handle(func(w http.ResponseWriter, r *http.Request) error {
		got := r.Header.Get("X-Internal-Token")
		ok := false
		for _, t := range s.internalTokens {
			if t != "" && got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(t)) == 1 {
				ok = true
			}
		}
		if !ok {
			return apperr.Forbidden("internal_only", nil)
		}
		return h(w, r)
	})
}

type internalOrderResp struct {
	orderResp
	PassengerID uuid.UUID `json:"passenger_id"`
}

func newInternalOrder(o domain.Order) internalOrderResp {
	return internalOrderResp{orderResp: newOrderResp(o), PassengerID: o.PassengerID}
}

// tz — admin sanalari O'zbekiston vaqtida (UTC+5): from_date 00:00 dan to_date+1 00:00 gacha.
var tz = time.FixedZone("UTC+5", int(service.StatsTZ.Seconds()))

func dateRange(r *http.Request, f map[string]string) (from, to *time.Time) {
	q := r.URL.Query()
	parse := func(key string) *time.Time {
		v := q.Get(key)
		if v == "" {
			return nil
		}
		t, err := time.ParseInLocation("2006-01-02", v, tz)
		if err != nil {
			f[key] = "invalid_format"
			return nil
		}
		return &t
	}
	from, to = parse("from_date"), parse("to_date")
	if to != nil {
		end := to.AddDate(0, 0, 1)
		to = &end
	}
	if from != nil && to != nil && !to.After(*from) {
		f["to_date"] = "before_from_date"
	}
	return from, to
}

func (s *Server) intOrders(w http.ResponseWriter, r *http.Request) error {
	cursor, limit, err := pageParams(r)
	if err != nil {
		return err
	}
	f := map[string]string{}
	var filter store.OrderFilter
	filter.From, filter.To = dateRange(r, f)
	if v := r.URL.Query().Get("status"); v != "" {
		switch domain.OrderStatus(v) {
		case domain.Searching, domain.Accepted, domain.Arrived, domain.InProgress, domain.Completed, domain.Cancelled, domain.Expired:
			filter.Status = &v
		default:
			f["status"] = "invalid_value"
		}
	}
	if v := r.URL.Query().Get("passenger_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			f["passenger_id"] = "invalid_format"
		}
		filter.Passenger = &id
	}
	if len(f) > 0 {
		return apperr.Validation(f)
	}
	p, err := s.svc.AdminOrders(r.Context(), filter, cursor, limit)
	if err != nil {
		return err
	}
	out := page[internalOrderResp]{Data: make([]internalOrderResp, 0, len(p.Items)), Cursor: p.Cursor}
	for _, o := range p.Items {
		out.Data = append(out.Data, newInternalOrder(o))
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) intOrder(w http.ResponseWriter, r *http.Request) error {
	oid, err := pathID(r)
	if err != nil {
		return err
	}
	o, err := s.svc.AdminOrder(r.Context(), oid)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newInternalOrder(o))
	return nil
}

type adminUpdateDTO struct {
	Comment       *string `json:"comment"`
	PaymentMethod *string `json:"payment_method"`
	ScheduledAt   *string `json:"scheduled_at"`
	ScheduledFrom *string `json:"scheduled_from"`
	ScheduledTo   *string `json:"scheduled_to"`

	at, from, to *time.Time
}

func (d *adminUpdateDTO) validate() map[string]string {
	f := map[string]string{}
	if d.Comment == nil && d.PaymentMethod == nil && d.ScheduledAt == nil && d.ScheduledFrom == nil && d.ScheduledTo == nil {
		return map[string]string{"body": "at_least_one_field"}
	}
	if d.PaymentMethod != nil && !domain.PaymentMethod(*d.PaymentMethod).Valid() {
		f["payment_method"] = "invalid_value"
	}
	checkComment(f, d.Comment)
	d.at = parseTime(f, "scheduled_at", d.ScheduledAt)
	d.from = parseTime(f, "scheduled_from", d.ScheduledFrom)
	d.to = parseTime(f, "scheduled_to", d.ScheduledTo)
	return f
}

func (s *Server) intUpdate(w http.ResponseWriter, r *http.Request) error {
	oid, err := pathID(r)
	if err != nil {
		return err
	}
	var in adminUpdateDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	upd := service.AdminUpdate{Comment: in.Comment, ScheduledAt: in.at, ScheduledFrom: in.from, ScheduledTo: in.to}
	if in.PaymentMethod != nil {
		m := domain.PaymentMethod(*in.PaymentMethod)
		upd.PaymentMethod = &m
	}
	o, err := s.svc.AdminUpdateOrder(r.Context(), oid, upd)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newInternalOrder(o))
	return nil
}

type adminCancelDTO struct {
	Reason  string  `json:"reason"`
	Comment *string `json:"comment"`
}

func (d *adminCancelDTO) validate() map[string]string {
	f := map[string]string{}
	switch {
	case d.Reason == "":
		f["reason"] = "required"
	case !service.AdminCancelReasons[d.Reason]:
		f["reason"] = "invalid_value"
	}
	if d.Comment != nil && utf8.RuneCountInString(*d.Comment) > 500 {
		f["comment"] = "too_long"
	}
	return f
}

func (s *Server) intCancel(w http.ResponseWriter, r *http.Request) error {
	oid, err := pathID(r)
	if err != nil {
		return err
	}
	var in adminCancelDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	o, err := s.svc.AdminCancelOrder(r.Context(), oid, in.Reason, in.Comment, requestID(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newInternalOrder(o))
	return nil
}

func (s *Server) intStats(w http.ResponseWriter, r *http.Request) error {
	f := map[string]string{}
	from, to := dateRange(r, f)
	if len(f) > 0 {
		return apperr.Validation(f)
	}
	st, err := s.svc.AdminStats(r.Context(), from, to)
	if err != nil {
		return err
	}
	type day struct {
		Date      string `json:"date"`
		Orders    int    `json:"orders"`
		Completed int    `json:"completed"`
		Revenue   int64  `json:"revenue"`
	}
	type route struct {
		FromCityID uuid.UUID `json:"from_city_id"`
		ToCityID   uuid.UUID `json:"to_city_id"`
		FromName   string    `json:"from_name"`
		ToName     string    `json:"to_name"`
		Orders     int       `json:"orders"`
	}
	daily := make([]day, 0, len(st.Daily))
	for _, d := range st.Daily {
		daily = append(daily, day(d))
	}
	routes := make([]route, 0, len(st.TopRoutes))
	for _, rt := range st.TopRoutes {
		routes = append(routes, route{FromCityID: rt.FromCityID, ToCityID: rt.ToCityID, FromName: rt.FromName, ToName: rt.ToName, Orders: rt.Orders})
	}
	byStatus := map[string]int{}
	for _, k := range []domain.OrderStatus{domain.Searching, domain.Accepted, domain.Arrived, domain.InProgress, domain.Completed, domain.Cancelled, domain.Expired} {
		byStatus[string(k)] = st.ByStatus[string(k)]
	}
	completed := st.ByStatus[string(domain.Completed)]
	var avg int64
	if completed > 0 {
		avg = st.Revenue / int64(completed)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"orders_total": st.Total, "orders_completed": completed, "orders_cancelled": st.ByStatus[string(domain.Cancelled)],
		"orders_expired": st.ByStatus[string(domain.Expired)], "orders_by_status": byStatus,
		"revenue": st.Revenue, "commission": st.Commission, "average_order_value": avg, "daily": daily, "top_routes": routes,
	})
	return nil
}
