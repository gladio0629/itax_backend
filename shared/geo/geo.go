// Package geo — masofa va davomiylik: Pricing va Matching&Geo uchun YAGONA formula (to'g'ri chiziq × yo'l koeffitsienti).
// Hozir Haversine; keyinroq Mapbox marshrutiga almashtiriladi (Router interfeysi o'sha payt uchun).
package geo

import (
	"context"
	"errors"
	"math"
)

type Point struct{ Lat, Lng float64 }

const earthRadiusM = 6371000.0

// Haversine — ikki nuqta orasidagi to'g'ri chiziq (katta aylana) masofasi, metrda.
func Haversine(a, b Point) float64 {
	rad := math.Pi / 180
	dLat := (b.Lat - a.Lat) * rad
	dLng := (b.Lng - a.Lng) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// Route — marshrut o'lchamlari.
type Route struct {
	DistanceM int
	DurationS int
}

// Router — marshrutni hisoblaydi (Haversine yoki keyinroq Mapbox).
type Router interface {
	Route(ctx context.Context, from, to Point) (Route, error)
}

// HaversineRouter — masofa = Haversine × RoadFactor (yo'l egriligi, default 1.3);
// davomiylik = masofa / AvgSpeedKMH. Ikkalasi ham taxminiy (Mapbox kelguncha).
type HaversineRouter struct {
	RoadFactor  float64
	AvgSpeedKMH float64
}

func NewHaversineRouter(roadFactor, avgSpeedKMH float64) (HaversineRouter, error) {
	if roadFactor < 1 || roadFactor > 3 {
		return HaversineRouter{}, errors.New("geo: road factor 1..3 oralig'ida bo'lishi kerak")
	}
	if avgSpeedKMH <= 0 || avgSpeedKMH > 200 {
		return HaversineRouter{}, errors.New("geo: o'rtacha tezlik 0..200 km/soat oralig'ida bo'lishi kerak")
	}
	return HaversineRouter{RoadFactor: roadFactor, AvgSpeedKMH: avgSpeedKMH}, nil
}

func (r HaversineRouter) Route(_ context.Context, from, to Point) (Route, error) {
	d := math.Round(Haversine(from, to) * r.RoadFactor)
	secs := math.Round(d / (r.AvgSpeedKMH / 3.6))
	return Route{DistanceM: int(d), DurationS: int(secs)}, nil
}

/*
Mapbox Directions API (keyinroq HaversineRouter o'rniga). Tayyor kod — MAPBOX_TOKEN muhitdan olinadi, kodga yozilmaydi.
Mapbox kelganda Pricing va Matching&Geo bir xil Router interfeysi orqali almashadi, shuning uchun ikkalasida narx/masofa mos qoladi.

	// MapboxRouter — https://docs.mapbox.com/api/navigation/directions/
	type MapboxRouter struct {
		Token  string       // MAPBOX_TOKEN
		Client *http.Client // timeout bilan (masalan 3 s); xatoda chaqiruvchi HaversineRouter'ga qaytishi mumkin
	}

	func (m MapboxRouter) Route(ctx context.Context, from, to Point) (Route, error) {
		// Mapbox koordinatani "lng,lat" tartibida kutadi.
		u := fmt.Sprintf("https://api.mapbox.com/directions/v5/mapbox/driving/%f,%f;%f,%f?overview=false&access_token=%s",
			from.Lng, from.Lat, to.Lng, to.Lat, url.QueryEscape(m.Token))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return Route{}, err
		}
		resp, err := m.Client.Do(req)
		if err != nil {
			return Route{}, fmt.Errorf("mapbox: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return Route{}, fmt.Errorf("mapbox: status %d", resp.StatusCode)
		}
		var body struct {
			Code   string `json:"code"`
			Routes []struct {
				Distance float64 `json:"distance"` // metr
				Duration float64 `json:"duration"` // soniya
			} `json:"routes"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return Route{}, fmt.Errorf("mapbox: %w", err)
		}
		if body.Code != "Ok" || len(body.Routes) == 0 {
			return Route{}, fmt.Errorf("mapbox: marshrut topilmadi (code=%q)", body.Code)
		}
		return Route{DistanceM: int(math.Round(body.Routes[0].Distance)), DurationS: int(math.Round(body.Routes[0].Duration))}, nil
	}
*/
