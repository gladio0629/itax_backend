package httpapi_test

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

// F-24 parallel yuklamada ham buzilmasligi kerak: bazadagi faol soni hech qachon limitdan oshmaydi.

func TestDriverLimitUnderConcurrentAccepts(t *testing.T) {
	e := newT(t, testenvPassengerLimit(10))
	for round := 0; round < 30; round++ {
		d := e.driver()
		e.AddDrivers(d.ID)
		const orders = 3
		offers := make([]string, orders)
		for i := range offers {
			offers[i] = d.offerFor(t, e.passenger().order(t, nil).str("id"))
		}
		e.MR.Del("matching:mock:drivers")

		var wg sync.WaitGroup
		start := make(chan struct{})
		codes := make([]int, orders)
		for i := range offers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				codes[i] = d.do("POST", acceptPath(offers[i]), nil).Status
			}(i)
		}
		close(start)
		wg.Wait()

		var active, ok int
		_ = e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM trips WHERE driver_id=$1 AND status IN ('assigned','in_progress')`, d.ID).Scan(&active)
		for _, c := range codes {
			if c == 200 {
				ok++
			} else if c != 403 {
				t.Fatalf("round %d: kutilmagan kod %v", round, codes)
			}
		}
		if active != 1 || ok != 1 {
			t.Fatalf("round %d: haydovchi limiti buzildi: faol safarlar=%d, 200 javoblar=%d (%v)", round, active, ok, codes)
		}
	}
}

func TestPassengerLimitUnderConcurrentCreates(t *testing.T) {
	e := newT(t)
	for round := 0; round < 30; round++ {
		p := e.passenger()
		const burst = 12
		var wg sync.WaitGroup
		start := make(chan struct{})
		codes := make([]int, burst)
		for i := 0; i < burst; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				codes[i] = p.do("POST", "/api/v1/orders", trip(nil)).Status
			}(i)
		}
		close(start)
		wg.Wait()

		var active, created int
		_ = e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM orders WHERE passenger_id=$1 AND status IN ('searching','accepted','in_progress')`, p.ID).Scan(&active)
		for _, c := range codes {
			switch c {
			case 201:
				created++
			case 403:
			default:
				t.Fatalf("round %d: kutilmagan kod %v", round, codes)
			}
		}
		if active != 3 || created != 3 {
			t.Fatalf("round %d: yo'lovchi limiti buzildi: faol=%d, 201=%d (%v)", round, active, created, codes)
		}
	}
}

// Bir xil Idempotency-Key bilan parallel takrorlar va oddiy so'rovlar aralash: limit va hisoblagich to'g'ri qoladi.
func TestPassengerLimitWithConcurrentReplays(t *testing.T) {
	e := newT(t)
	for round := 0; round < 15; round++ {
		p := e.passenger()
		keyed := p.with("Idempotency-Key", uuid.NewString())
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if i%2 == 0 {
					keyed.do("POST", "/api/v1/orders", trip(nil))
				} else {
					p.do("POST", "/api/v1/orders", trip(nil))
				}
			}(i)
		}
		close(start)
		wg.Wait()
		var active int
		_ = e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM orders WHERE passenger_id=$1 AND status IN ('searching','accepted','in_progress')`, p.ID).Scan(&active)
		if active > 3 {
			t.Fatalf("round %d: faol buyurtmalar %d > 3", round, active)
		}
		// hisoblagich bazaga mos kelishi shart emas (maslahat), lekin keyingi sekvensial so'rov to'g'ri qaror chiqarishi shart
		r := p.do("POST", "/api/v1/orders", trip(nil))
		if (active == 3) != (r.Status == 403) {
			t.Fatalf("round %d: active=%d, keyingi so'rov %d", round, active, r.Status)
		}
	}
}
