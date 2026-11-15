package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/izgo/shared/go/authn"
	"github.com/izgo/trip-order/internal/apperr"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/izgo/trip-order/internal/service"
)

// ---- narx taklifi

type quoteResp struct {
	QuoteID   uuid.UUID          `json:"quote_id"`
	Price     int64              `json:"price"`
	Lines     []domain.PriceLine `json:"lines"`
	DistanceM int                `json:"distance_m"`
	DurationS int                `json:"duration_s"`
	ExpiresAt time.Time          `json:"expires_at"`
}

func (s *Server) quote(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	var in quoteDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	q, err := s.svc.QuoteOrder(r.Context(), id.UserID, in.input())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, quoteResp{QuoteID: q.ID, Price: q.Price, Lines: q.Lines, DistanceM: q.DistanceM, DurationS: q.DurationS, ExpiresAt: q.ExpiresAt.UTC()})
	return nil
}

// ---- shaharlar

type cityResp struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Region string    `json:"region"`
	Lat    float64   `json:"lat"`
	Lng    float64   `json:"lng"`
}

func newCityResp(c domain.City) cityResp {
	return cityResp{ID: c.ID, Name: c.Name, Region: c.Region, Lat: c.Lat, Lng: c.Lng}
}

func (s *Server) cities(w http.ResponseWriter, r *http.Request) error {
	cs, err := s.svc.Cities(r.Context())
	if err != nil {
		return err
	}
	out := make([]cityResp, 0, len(cs))
	for _, c := range cs {
		out = append(out, newCityResp(c))
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

// ---- manzil qidiruvi

type placeResp struct {
	ID        uuid.UUID `json:"id"`
	CityID    uuid.UUID `json:"city_id"`
	Name      string    `json:"name"`
	Area      string    `json:"area"`
	Kind      string    `json:"kind"`
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	Entrance  *string   `json:"entrance"`
	DistanceM *int      `json:"distance_m"`
}

func newPlaceResp(p domain.Place) placeResp {
	return placeResp{ID: p.ID, CityID: p.CityID, Name: p.Name, Area: p.Area, Kind: p.Kind, Lat: p.Lat, Lng: p.Lng, Entrance: p.Entrance, DistanceM: p.DistanceM}
}

// queryPoint — ?<prefix>lat=&<prefix>lng= (ikkalasi birga). Yo'q bo'lsa nil.
func queryPoint(r *http.Request, f map[string]string, latKey, lngKey string, required bool) *domain.Point {
	q := r.URL.Query()
	ls, gs := q.Get(latKey), q.Get(lngKey)
	if ls == "" && gs == "" {
		if required {
			f[latKey] = "required"
		}
		return nil
	}
	lat, err1 := strconv.ParseFloat(ls, 64)
	lng, err2 := strconv.ParseFloat(gs, 64)
	if err1 != nil || math.IsNaN(lat) || lat < -90 || lat > 90 {
		f[latKey] = "invalid_value"
	}
	if err2 != nil || math.IsNaN(lng) || lng < -180 || lng > 180 {
		f[lngKey] = "invalid_value"
	}
	if err1 != nil || err2 != nil {
		return nil
	}
	return &domain.Point{Lat: lat, Lng: lng}
}

func (s *Server) placesSearch(w http.ResponseWriter, r *http.Request, _ *authn.Identity) error {
	f := map[string]string{}
	q := r.URL.Query()
	in := service.SearchInput{Q: q.Get("q"), Limit: 20}
	if utf8.RuneCountInString(in.Q) > 100 {
		f["q"] = "too_long"
	}
	in.Near = queryPoint(r, f, "near_lat", "near_lng", false)
	if v := q.Get("city_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			f["city_id"] = "invalid_format"
		}
		in.CityID = &id
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 50 {
			f["limit"] = "out_of_range"
		}
		in.Limit = n
	}
	if len(f) > 0 {
		return apperr.Validation(f)
	}
	ps, err := s.svc.SearchPlaces(r.Context(), in)
	if err != nil {
		return err
	}
	out := make([]placeResp, 0, len(ps))
	for _, p := range ps {
		out = append(out, newPlaceResp(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Server) placesReverse(w http.ResponseWriter, r *http.Request, _ *authn.Identity) error {
	f := map[string]string{}
	p := queryPoint(r, f, "lat", "lng", true)
	if len(f) > 0 {
		return apperr.Validation(f)
	}
	pl, err := s.svc.ReversePlace(r.Context(), *p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, newPlaceResp(pl))
	return nil
}

// ---- saqlangan manzillar

type savedResp struct {
	ID        uuid.UUID         `json:"id"`
	Name      string            `json:"name"`
	Area      string            `json:"area"`
	Lat       float64           `json:"lat"`
	Lng       float64           `json:"lng"`
	Details   map[string]string `json:"details"`
	CreatedAt time.Time         `json:"created_at"`
}

func newSavedResp(p domain.SavedPlace) savedResp {
	return savedResp{ID: p.ID, Name: p.Name, Area: p.Area, Lat: p.Lat, Lng: p.Lng, Details: p.Details, CreatedAt: p.CreatedAt}
}

type savedDTO struct {
	Name    string    `json:"name"`
	Area    string    `json:"area"`
	Lat     *float64  `json:"lat"`
	Lng     *float64  `json:"lng"`
	Details *struct { // hammasi ixtiyoriy matn
		Entrance  *string `json:"entrance"`
		Floor     *string `json:"floor"`
		Apartment *string `json:"apartment"`
		Intercom  *string `json:"intercom"`
		Note      *string `json:"note"`
	} `json:"details"`

	details map[string]string
}

func (d *savedDTO) validate() map[string]string {
	f := map[string]string{}
	d.Name, d.Area = strings.TrimSpace(d.Name), strings.TrimSpace(d.Area)
	if n := utf8.RuneCountInString(d.Name); n < 1 || n > 100 {
		f["name"] = "invalid_length"
	}
	if utf8.RuneCountInString(d.Area) > 200 {
		f["area"] = "too_long"
	}
	(&pointDTO{Lat: d.Lat, Lng: d.Lng}).check(f, "point")
	if f["point.lat"] != "" {
		f["lat"] = f["point.lat"]
	}
	if f["point.lng"] != "" {
		f["lng"] = f["point.lng"]
	}
	delete(f, "point.lat")
	delete(f, "point.lng")
	if d.Details != nil {
		d.details = map[string]string{}
		for k, v := range map[string]*string{"entrance": d.Details.Entrance, "floor": d.Details.Floor, "apartment": d.Details.Apartment,
			"intercom": d.Details.Intercom, "note": d.Details.Note} {
			if v == nil {
				continue
			}
			t := strings.TrimSpace(*v)
			max := 20
			if k == "note" {
				max = 200
			}
			if utf8.RuneCountInString(t) > max {
				f["details."+k] = "too_long"
			} else if t != "" {
				d.details[k] = t
			}
		}
	}
	return f
}

func (s *Server) savedList(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	ps, err := s.svc.SavedPlaces(r.Context(), id.UserID)
	if err != nil {
		return err
	}
	out := make([]savedResp, 0, len(ps))
	for _, p := range ps {
		out = append(out, newSavedResp(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Server) savedCreate(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	var in savedDTO
	if err := decode(w, r, &in); err != nil {
		return err
	}
	p, err := s.svc.SavePlace(r.Context(), domain.SavedPlace{UserID: id.UserID, Name: in.Name, Area: in.Area, Lat: *in.Lat, Lng: *in.Lng, Details: in.details})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, newSavedResp(p))
	return nil
}

func (s *Server) savedDelete(w http.ResponseWriter, r *http.Request, id *authn.Identity) error {
	pid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.NotFound()
	}
	if err := s.svc.DeleteSavedPlace(r.Context(), id.UserID, pid); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- mashhur yo'nalishlar, yuk mashinalari, seat map

func (s *Server) popularRoutes(w http.ResponseWriter, r *http.Request) error {
	rs, err := s.svc.PopularRoutes(r.Context())
	if err != nil {
		return err
	}
	type end struct {
		CityID  uuid.UUID `json:"city_id"`
		Lat     float64   `json:"lat"`
		Lng     float64   `json:"lng"`
		Address string    `json:"address"`
	}
	type route struct {
		From      end    `json:"from"`
		To        end    `json:"to"`
		DistanceM *int   `json:"distance_m"`
		PriceFrom *int64 `json:"price_from"`
	}
	out := make([]route, 0, len(rs))
	for _, v := range rs {
		a, b := v.Route.From, v.Route.To
		out = append(out, route{
			From: end{CityID: a.ID, Lat: a.Lat, Lng: a.Lng, Address: a.Name}, To: end{CityID: b.ID, Lat: b.Lat, Lng: b.Lng, Address: b.Name},
			DistanceM: v.DistanceM, PriceFrom: v.PriceFrom,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Server) deliveryVehicles(w http.ResponseWriter, _ *http.Request) error {
	type dims struct {
		Width  int `json:"width"`
		Depth  int `json:"depth"`
		Height int `json:"height"`
	}
	type vehicle struct {
		VehicleType  string `json:"vehicle_type"`
		SizeLabel    string `json:"size_label"`
		DimensionsCm dims   `json:"dimensions_cm"`
		MaxWeightKg  int    `json:"max_weight_kg"`
	}
	out := make([]vehicle, 0, len(service.DeliveryVehicles))
	for _, v := range service.DeliveryVehicles {
		out = append(out, vehicle{VehicleType: string(v.Type), SizeLabel: v.SizeLabel,
			DimensionsCm: dims{Width: v.WidthCm, Depth: v.DepthCm, Height: v.HeightCm}, MaxWeightKg: v.MaxWeightKg})
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (s *Server) seatMap(w http.ResponseWriter, r *http.Request, _ *authn.Identity) error {
	f := map[string]string{}
	from := queryPoint(r, f, "from_lat", "from_lng", true)
	to := queryPoint(r, f, "to_lat", "to_lng", true)
	if len(f) > 0 {
		return apperr.Validation(f)
	}
	seats, err := s.svc.SeatMap(r.Context(), *from, *to)
	if err != nil {
		return err
	}
	type seat struct {
		ID    string `json:"id"`
		Taken bool   `json:"taken"`
		Price int64  `json:"price"`
	}
	out := make([]seat, 0, len(seats))
	for _, v := range seats {
		out = append(out, seat{ID: string(v.ID), Taken: v.Taken, Price: v.Price})
	}
	writeJSON(w, http.StatusOK, map[string]any{"seats": out})
	return nil
}
