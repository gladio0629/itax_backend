// Package service — narx hisoblash: tarif (kesh/DB) + marshrut (Haversine) + formula.
package service

import (
	"context"
	"fmt"

	"github.com/izgo/pricing/internal/calc"
	"github.com/izgo/pricing/internal/tariff"
	"github.com/izgo/shared/geo"
)

type TariffProvider interface {
	Active(ctx context.Context) (tariff.Tariff, error)
}

type Service struct {
	tariffs TariffProvider
	router  geo.Router
}

func New(t TariffProvider, r geo.Router) *Service { return &Service{tariffs: t, router: r} }

type Request struct {
	Type        calc.OrderType
	Seats       *int
	CarClass    *string
	VehicleType *string
	From, To    geo.Point
}

type Quote struct {
	Price         int64 // tiyin
	Commission    int64 // tiyin
	DistanceM     int
	DurationS     int
	TariffVersion int
	// Lines — narx tarkibi (yig'indisi = Price).
	Lines []calc.Line
}

func (s *Service) Calculate(ctx context.Context, r Request) (Quote, error) {
	t, err := s.tariffs.Active(ctx)
	if err != nil {
		return Quote{}, fmt.Errorf("pricing: tarif: %w", err)
	}
	rt, err := s.router.Route(ctx, r.From, r.To)
	if err != nil {
		return Quote{}, fmt.Errorf("pricing: marshrut: %w", err)
	}
	in := calc.Input{Type: r.Type, Seats: r.Seats, CarClass: r.CarClass, VehicleType: r.VehicleType, DistanceM: rt.DistanceM}
	res, err := calc.Price(t, in)
	if err != nil {
		return Quote{}, err
	}
	return Quote{Price: res.Price, Commission: res.Commission, DistanceM: rt.DistanceM, DurationS: rt.DurationS, TariffVersion: t.Version,
		Lines: calc.Lines(t, in, res.Price)}, nil
}
