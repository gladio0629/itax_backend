package store_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/izgo/trip-order/internal/domain"
	"github.com/izgo/trip-order/internal/events"
	"github.com/izgo/trip-order/internal/service"
	"github.com/izgo/trip-order/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }

func ev() func(o, t uuid.UUID) (events.Event, error) {
	return func(o, t uuid.UUID) (events.Event, error) {
		return events.OrderAccepted(o, t, uuid.New(), uuid.New(), time.Now(), "")
	}
}

// Redis qulfi va servis oldindan-tekshiruvisiz, to'g'ridan-to'g'ri bazaga: atomar
// `UPDATE ... WHERE status='searching'` o'zi "birinchi qabul qilgan oladi"ni kafolatlashi kerak.
func TestAcceptOfferAtomicInDB(t *testing.T) {
	e := testenv.New(t)
	ctx := context.Background()
	const n = 16
	drivers := make([]uuid.UUID, n)
	for i := range drivers {
		drivers[i] = uuid.New()
		e.AddDrivers(drivers[i])
	}
	seats := 2
	o, err := e.Svc.CreateOrder(ctx, uuid.New(), service.CreateInput{
		Type: domain.TypePassenger, Seats: &seats, PaymentMethod: domain.Cash,
		From: domain.Point{Lat: 41.31, Lng: 69.27}, To: domain.Point{Lat: 41.29, Lng: 69.24},
	}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	offerIDs := make([]uuid.UUID, n)
	for i, d := range drivers {
		page, err := e.Svc.Offers(ctx, d, "", 10)
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("driver %d: %v %d", i, err, len(page.Items))
		}
		offerIDs[i] = page.Items[0].Offer.ID
	}

	var wg sync.WaitGroup
	var won, taken, other atomic.Int32
	start := make(chan struct{})
	for i := range drivers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := e.Store.AcceptOffer(ctx, offerIDs[i], drivers[i], time.Now(), 1, ev())
			switch {
			case err == nil:
				won.Add(1)
			case errors.Is(err, domain.ErrAlreadyTaken):
				taken.Add(1)
			default:
				other.Add(1)
				t.Errorf("driver %d: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if won.Load() != 1 || taken.Load() != n-1 {
		t.Fatalf("won=%d taken=%d other=%d", won.Load(), taken.Load(), other.Load())
	}
	var trips, accepted, events int
	_ = e.Pool.QueryRow(ctx, `SELECT count(*) FROM trips WHERE order_id=$1`, o.ID).Scan(&trips)
	_ = e.Pool.QueryRow(ctx, `SELECT count(*) FROM offers WHERE order_id=$1 AND status='accepted'`, o.ID).Scan(&accepted)
	_ = e.Pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE subject='izgo.order.accepted.v1'`).Scan(&events)
	if trips != 1 || accepted != 1 || events != 1 {
		t.Errorf("trips=%d accepted=%d events=%d", trips, accepted, events)
	}
}

// Bekor qilish va qabul qilish bir vaqtda: ikkalasi ham g'olib bo'la olmaydi.
func TestCancelVsAcceptRace(t *testing.T) {
	e := testenv.New(t)
	ctx := context.Background()
	for round := 0; round < 20; round++ {
		d, p := uuid.New(), uuid.New()
		e.AddDrivers(d)
		seats := 1
		o, err := e.Svc.CreateOrder(ctx, p, service.CreateInput{
			Type: domain.TypePassenger, Seats: &seats, PaymentMethod: domain.Cash,
			From: domain.Point{Lat: 41.31, Lng: 69.27}, To: domain.Point{Lat: 41.29, Lng: 69.24},
		}, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		page, _ := e.Svc.Offers(ctx, d, "", 100)
		var offer uuid.UUID
		for _, it := range page.Items {
			if it.Order.ID == o.ID {
				offer = it.Offer.ID
			}
		}
		e.MR.Del("matching:mock:drivers")

		var wg sync.WaitGroup
		var accErr, cancelErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, accErr = e.Store.AcceptOffer(ctx, offer, d, time.Now(), 1, ev()) }()
		go func() {
			defer wg.Done()
			_, _, cancelErr = e.Store.CancelOrder(ctx, o.ID, p, nil, nil, time.Now())
		}()
		wg.Wait()

		// Aynan bittasi g'olib: qabul g'olib bo'lsa bekor qilish StateError, bekor qilish g'olib bo'lsa qabul ErrNotFound.
		accepted, cancelled := accErr == nil, cancelErr == nil
		if accepted == cancelled {
			t.Fatalf("round %d: accErr=%v cancelErr=%v (aynan bittasi muvaffaqiyatli bo'lishi kerak)", round, accErr, cancelErr)
		}
		got, _ := e.Store.GetOrder(ctx, o.ID)
		want := domain.Cancelled
		if accepted {
			want = domain.Accepted
		}
		if got.Status != want {
			t.Fatalf("round %d: status=%s, kutilgan %s", round, got.Status, want)
		}
		// yo'lovchi tashqi hisoblagichi: cancelled bo'lsa bo'shatilmaydi (store darajasi) — faqat tozalash uchun
		_ = e.Guard.Release(ctx, "p", p)
	}
}
