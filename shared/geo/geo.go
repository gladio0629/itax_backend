// Package geo — masofa va davomiylik: Pricing va Matching&Geo uchun YAGONA manba (Router interfeysi).
// MapboxRouter — real yo'l (Mapbox Directions, mapbox.go); HaversineRouter — to'g'ri chiziq × yo'l koeffitsienti
// (Mapbox o'chirilgan yoki javob bermaganda). Tanlash: NewRouter.
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

// Router — marshrutni hisoblaydi (MapboxRouter, HaversineRouter yoki ikkalasi — FallbackRouter).
type Router interface {
	Route(ctx context.Context, from, to Point) (Route, error)
}

// HaversineRouter — masofa = Haversine × RoadFactor (yo'l egriligi, default 1.3);
// davomiylik = masofa / AvgSpeedKMH. Ikkalasi ham taxminiy: Mapbox yo'q yoki javob bermaganda ishlatiladi.
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
