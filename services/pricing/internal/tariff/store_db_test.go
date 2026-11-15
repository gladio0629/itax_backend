package tariff_test

import (
	"context"
	"os"
	"testing"

	"github.com/izgo/pricing/internal/tariff"
	"github.com/izgo/pricing/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

// Boshlang'ich (taklif) tarif migratsiya bilan keladi va yaroqli.
func TestSeededTariff(t *testing.T) {
	e := testenv.New(t)
	got, err := tariff.NewStore(e.Pool).Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.BaseFare != 400_000 || got.PerKmRate != 120_000 || got.SeatMultiplier != 1000 ||
		got.CommissionBPS != 1000 || got.MinFare != 0 || got.RoundingStep != 1 || got.FuelSurcharge != 0 {
		t.Errorf("tarif: %+v", got)
	}
	// vazifada keltirilgan delivery koeffitsientlari
	for k, v := range map[string]int64{"spark": 1000, "damas": 1300, "labo": 1500, "isuzu": 2000, "isuzu2": 2500, "fura": 3000} {
		if got.VehicleMultipliers[k] != v {
			t.Errorf("%s = %d, kutilgan %d", k, got.VehicleMultipliers[k], v)
		}
	}
}

func TestSchemaConstraints(t *testing.T) {
	e := testenv.New(t)
	ctx := context.Background()
	ins := func(version int, active bool, car, veh string, extra string) error {
		_, err := e.Pool.Exec(ctx, `INSERT INTO tariffs (id, version, active, base_fare, per_km_rate, car_class_multipliers, vehicle_multipliers `+extra+`)
			VALUES (gen_random_uuid(), $1, $2, 1, 1, $3::jsonb, $4::jsonb)`, version, active, car, veh)
		return err
	}
	car := `{"economy":1,"comfort":1,"business":1,"minivan":1}`
	veh := `{"spark":1,"damas":1,"labo":1,"isuzu":1,"isuzu2":1,"fura":1}`

	if err := ins(2, false, car, veh, ""); err != nil {
		t.Fatalf("yaroqli passiv tarif: %v", err)
	}
	if err := ins(3, true, car, veh, ""); err == nil {
		t.Error("ikkinchi amaldagi (active) tarifga ruxsat berildi")
	}
	if err := ins(2, false, car, veh, ""); err == nil {
		t.Error("takroriy version")
	}
	if err := ins(4, false, `{"economy":1}`, veh, ""); err == nil {
		t.Error("car_class kalitlari yetishmaydi, lekin qabul qilindi")
	}
	if err := ins(5, false, car, `{"spark":1}`, ""); err == nil {
		t.Error("vehicle kalitlari yetishmaydi, lekin qabul qilindi")
	}
	if err := ins(6, false, `[1]`, veh, ""); err == nil {
		t.Error("massiv multipliers qabul qilindi")
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE tariffs SET commission_bps = 20000 WHERE version = 2`); err == nil {
		t.Error("komissiya > 100% qabul qilindi")
	}
	if _, err := e.Pool.Exec(ctx, `UPDATE tariffs SET base_fare = -1 WHERE version = 2`); err == nil {
		t.Error("manfiy base_fare qabul qilindi")
	}

	// amaldagi tarif almashtirilganda (tx) yangi versiya o'qiladi
	if _, err := e.Pool.Exec(ctx, `UPDATE tariffs SET active = (version = 2)`); err != nil {
		t.Fatal(err)
	}
	got, err := tariff.NewStore(e.Pool).Active(ctx)
	if err != nil || got.Version != 2 {
		t.Fatalf("v2 faol bo'lishi kerak: %+v %v", got, err)
	}
	// hech biri faol emas
	_, _ = e.Pool.Exec(ctx, `UPDATE tariffs SET active = false`)
	if _, err := tariff.NewStore(e.Pool).Active(ctx); err != tariff.ErrNoActiveTariff {
		t.Errorf("faol tarif yo'q: %v", err)
	}
	// jadvalga qo'lda yozilgan, lekin qiymati yaroqsiz tarif (0 ko'paytirgich) o'qishda rad etiladi
	if _, err := e.Pool.Exec(ctx, `UPDATE tariffs SET active = true, vehicle_multipliers = '{"spark":0,"damas":1,"labo":1,"isuzu":1,"isuzu2":1,"fura":1}' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := tariff.NewStore(e.Pool).Active(ctx); err == nil {
		t.Error("yaroqsiz tarif (spark=0) qabul qilindi")
	}
}
