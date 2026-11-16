package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/izgo/shared/go/migrate"
	"io/fs"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/google/uuid"
	"github.com/izgo/identity/internal/auth"
	"github.com/izgo/identity/internal/events"
	"github.com/izgo/identity/internal/postgres"
	"github.com/izgo/identity/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Haqiqiy PostgreSQL kerak. Ikki yo'l:
//
//	IDENTITY_TEST_DSN=postgres://...   — tayyor (bo'sh) baza
//	IDENTITY_TEST_PG=1                 — embedded-postgres (birinchi marta binary yuklab oladi)
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("IDENTITY_TEST_DSN")
	if dsn == "" {
		if os.Getenv("IDENTITY_TEST_PG") == "" {
			t.Skip("IDENTITY_TEST_PG=1 yoki IDENTITY_TEST_DSN berilmagan")
		}
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := uint32(l.Addr().(*net.TCPAddr).Port)
		_ = l.Close()
		dir := t.TempDir()
		pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
			Port(port).Database("identity_db").Username("identity_svc").Password("test").
			RuntimePath(dir + "/run").DataPath(dir + "/data").BinariesPath(os.Getenv("HOME") + "/.cache/identity-embedded-pg"))
		if err := pg.Start(); err != nil {
			t.Skipf("embedded postgres ishga tushmadi: %v", err)
		}
		t.Cleanup(func() { _ = pg.Stop() })
		dsn = fmt.Sprintf("postgres://identity_svc:test@127.0.0.1:%d/identity_db?sslmode=disable", port)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Run(ctx, pool, migrations.FS, migrations.LockID); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func ptr(s string) *string { return &s }

func cand(phone, role string) auth.User {
	return auth.User{ID: uuid.Must(uuid.NewV7()), Phone: phone, Role: "passenger", Settings: auth.DefaultSettings(), CreatedAt: time.Now().UTC()}
}

func newEvent(u auth.User) (events.Event, error) {
	return events.NewUserCreated(u.ID, u.Phone, string(u.Role), u.CreatedAt, time.Now(), "trace-1")
}

func TestPostgres(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	repo := postgres.NewRepo(pool)

	t.Run("migrate idempotent", func(t *testing.T) {
		if err := migrate.Run(ctx, pool, migrations.FS, migrations.LockID); err != nil {
			t.Fatal(err)
		}
		files, err := fs.Glob(migrations.FS, "*.up.sql")
		if err != nil || len(files) < 2 {
			t.Fatalf("migratsiya fayllari: %v %v", files, err)
		}
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n)
		if n != len(files) {
			t.Fatalf("schema_migrations: %d, fayllar: %d", n, len(files))
		}
	})

	t.Run("find or create user + outbox bir tranzaksiyada", func(t *testing.T) {
		u1, created, err := repo.FindOrCreateUser(ctx, cand("+998901000001", "passenger"), newEvent)
		if err != nil || !created {
			t.Fatalf("created=%v err=%v", created, err)
		}
		u2, created, err := repo.FindOrCreateUser(ctx, cand("+998901000001", "passenger"), newEvent)
		if err != nil || created || u2.ID != u1.ID {
			t.Fatalf("ikkinchi chaqiruv: created=%v id=%v/%v err=%v", created, u2.ID, u1.ID, err)
		}
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE payload->'payload'->>'user_id' = $1`, u1.ID.String()).Scan(&n)
		if n != 1 {
			t.Fatalf("outbox: %d ta hodisa", n)
		}
		if u1.Settings != auth.DefaultSettings() {
			t.Fatalf("settings: %+v", u1.Settings)
		}
	})

	t.Run("parallel kirish bitta foydalanuvchi yaratadi", func(t *testing.T) {
		var wg sync.WaitGroup
		var createdN atomic.Int32
		ids := make(chan uuid.UUID, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				u, created, err := repo.FindOrCreateUser(ctx, cand("+998901000002", "passenger"), newEvent)
				if err != nil {
					t.Error(err)
					return
				}
				if created {
					createdN.Add(1)
				}
				ids <- u.ID
			}()
		}
		wg.Wait()
		close(ids)
		first := uuid.Nil
		for id := range ids {
			if first == uuid.Nil {
				first = id
			}
			if id != first {
				t.Fatal("turli ID lar qaytdi")
			}
		}
		if createdN.Load() != 1 {
			t.Fatalf("%d marta yaratildi", createdN.Load())
		}
	})

	t.Run("bitta telefon ikki rolda", func(t *testing.T) {
		d := cand("+998901000003", "driver")
		d.Role = "driver"
		a, _, _ := repo.FindOrCreateUser(ctx, d, newEvent)
		p, created, err := repo.FindOrCreateUser(ctx, cand("+998901000003", "passenger"), newEvent)
		if err != nil || !created || a.ID == p.ID {
			t.Fatalf("created=%v err=%v", created, err)
		}
	})

	t.Run("profil va sozlamalar", func(t *testing.T) {
		u, _, _ := repo.FindOrCreateUser(ctx, cand("+998901000004", "passenger"), newEvent)
		first, last := "Ali", "Vali"
		got, err := repo.UpdateUserName(ctx, u.ID, &first, nil)
		if err != nil || *got.FirstName != "Ali" || got.LastName != nil {
			t.Fatalf("%+v %v", got, err)
		}
		got, _ = repo.UpdateUserName(ctx, u.ID, nil, &last)
		if *got.FirstName != "Ali" || *got.LastName != "Vali" {
			t.Fatalf("COALESCE ishlamadi: %+v", got)
		}
		s := auth.DefaultSettings()
		s.Language, s.Notifications.Promo = "ru", false
		if err := repo.UpdateUserSettings(ctx, u.ID, s); err != nil {
			t.Fatal(err)
		}
		again, _ := repo.GetUser(ctx, u.ID)
		if again.Settings != s {
			t.Fatalf("settings: %+v", again.Settings)
		}
		if _, err := repo.GetUser(ctx, uuid.New()); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("topilmadi: %v", err)
		}
	})

	t.Run("refresh rotation: atomik, reuse aniqlanadi", func(t *testing.T) {
		u, _, _ := repo.FindOrCreateUser(ctx, cand("+998901000005", "passenger"), newEvent)
		fam := uuid.New()
		mk := func() auth.RefreshToken {
			return auth.RefreshToken{JTI: uuid.Must(uuid.NewV7()), UserID: u.ID, Family: fam, ExpiresAt: time.Now().Add(time.Hour)}
		}
		t0 := mk()
		if err := repo.InsertRefresh(ctx, t0); err != nil {
			t.Fatal(err)
		}

		// 10 ta parallel urinish: aynan bittasi Rotated
		var wg sync.WaitGroup
		var rotated, reused atomic.Int32
		var winner atomic.Value
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				next := mk()
				out, owner, err := repo.RotateRefresh(ctx, t0.JTI, next)
				if err != nil {
					t.Error(err)
					return
				}
				switch out {
				case auth.Rotated:
					rotated.Add(1)
					winner.Store(next)
				case auth.Reused:
					reused.Add(1)
					if owner != u.ID {
						t.Error("reuse egasi noto'g'ri")
					}
				}
			}()
		}
		wg.Wait()
		if rotated.Load() != 1 || reused.Load() != 9 {
			t.Fatalf("rotated=%d reused=%d", rotated.Load(), reused.Load())
		}

		// reuse: hamma bekor qilinadi, oila qaytadi
		fams, err := repo.RevokeAllForUser(ctx, u.ID)
		if err != nil || len(fams) != 1 || fams[0] != fam {
			t.Fatalf("families=%v err=%v", fams, err)
		}
		w := winner.Load().(auth.RefreshToken)
		if out, _, _ := repo.RotateRefresh(ctx, w.JTI, mk()); out != auth.Rejected {
			t.Fatalf("bekor qilingan token: %v", out)
		}
		// noma'lum jti
		if out, _, _ := repo.RotateRefresh(ctx, uuid.New(), mk()); out != auth.Rejected {
			t.Fatalf("noma'lum token: %v", out)
		}
		// muddati o'tgan
		exp := auth.RefreshToken{JTI: uuid.New(), UserID: u.ID, Family: uuid.New(), ExpiresAt: time.Now().Add(-time.Minute)}
		_ = repo.InsertRefresh(ctx, exp)
		if out, _, _ := repo.RotateRefresh(ctx, exp.JTI, mk()); out != auth.Rejected {
			t.Fatalf("muddati o'tgan token: %v", out)
		}
		// logout (oila bekor) — "reuse" emas
		lo := mk()
		lo.Family = uuid.New()
		_ = repo.InsertRefresh(ctx, lo)
		_ = repo.RevokeFamily(ctx, lo.Family)
		if out, _, _ := repo.RotateRefresh(ctx, lo.JTI, mk()); out != auth.Rejected {
			t.Fatalf("logout qilingan token: %v", out)
		}
	})

	t.Run("outbox: yuborilganlar belgilanadi, xatoda qolganlari qoladi", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `DELETE FROM events_outbox`); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			repo.FindOrCreateUser(ctx, cand(fmt.Sprintf("+99890200000%d", i), "passenger"), newEvent) //nolint:errcheck
		}
		var calls int
		failOn := 2
		n, err := repo.DrainOutbox(ctx, 10, func(_ context.Context, e events.Event) error {
			calls++
			if calls == failOn {
				return errors.New("nats yo'q")
			}
			return nil
		})
		if err == nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		var pending int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE published_at IS NULL`).Scan(&pending)
		if pending != 2 {
			t.Fatalf("pending=%d", pending)
		}
		n, err = repo.DrainOutbox(ctx, 10, func(context.Context, events.Event) error { return nil })
		if err != nil || n != 2 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if n, _ = repo.DrainOutbox(ctx, 10, func(context.Context, events.Event) error { return nil }); n != 0 {
			t.Fatalf("takror yuborildi: %d", n)
		}
	})

	t.Run("staff", func(t *testing.T) {
		id := uuid.New()
		s := auth.Staff{ID: id, Email: ptr("boss@izgo.uz"), Phone: "+998901112233", FullName: "Boss", Role: "superadmin", Active: true}
		if err := repo.EnsureStaff(ctx, s); err != nil {
			t.Fatal(err)
		}
		dup := s
		dup.ID, dup.Email, dup.Phone = uuid.New(), ptr("BOSS@izgo.uz"), "+998901112299"
		if err := repo.EnsureStaff(ctx, dup); err != nil { // case-insensitive unique: jim o'tkazib yuboriladi
			t.Fatal(err)
		}
		got, err := repo.GetStaffByEmail(ctx, "Boss@IZGO.uz")
		if err != nil || got.ID != id || got.Role != "superadmin" || !got.Active {
			t.Fatalf("%+v %v", got, err)
		}
		if _, err := repo.GetStaffByEmail(ctx, "no@izgo.uz"); !errors.Is(err, auth.ErrNotFound) {
			t.Fatal(err)
		}
		if err := repo.TouchStaffLogin(ctx, id, time.Now()); err != nil {
			t.Fatal(err)
		}
		// InsertEvent: outbox'ga yoziladi, bir xil ID (idempotent logout) — jim o'tadi, ikki marta yuborilmaydi
		lo, err := events.NewStaffLoggedOut(id, "superadmin", "sid-1", time.Now(), "")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err := repo.InsertEvent(ctx, lo); err != nil {
				t.Fatalf("InsertEvent %d: %v", i, err)
			}
		}
		var cnt int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM events_outbox WHERE id = $1 AND published_at IS NULL`, lo.ID).Scan(&cnt); err != nil || cnt != 1 {
			t.Fatalf("outbox: %d %v", cnt, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM events_outbox WHERE id = $1`, lo.ID); err != nil {
			t.Fatal(err)
		}
		bad := s
		bad.ID, bad.Email, bad.Phone, bad.Role = uuid.New(), ptr("x@izgo.uz"), "+998901112200", "root"
		if err := repo.EnsureStaff(ctx, bad); err == nil {
			t.Fatal("noma'lum rol CHECK'dan o'tib ketdi")
		}
	})

	t.Run("staff: email NULL (operator), unikal telefon, CreateStaff xatolari", func(t *testing.T) {
		op1 := auth.Staff{ID: uuid.New(), Phone: "+998907770001", FullName: "Op1", Role: "operator", Active: true}
		op2 := auth.Staff{ID: uuid.New(), Phone: "+998907770002", FullName: "Op2", Role: "operator", Active: true}
		// ikki email'siz operator: NULL lar unikal indeksda to'qnashmaydi
		for _, op := range []auth.Staff{op1, op2} {
			if err := repo.CreateStaff(ctx, op); err != nil {
				t.Fatal(err)
			}
		}
		got, err := repo.GetStaffByPhone(ctx, op1.Phone)
		if err != nil || got.ID != op1.ID || got.Email != nil || got.Role != "operator" {
			t.Fatalf("NULL email round-trip: %+v %v", got, err)
		}
		if got, err = repo.GetStaff(ctx, op2.ID); err != nil || got.Email != nil {
			t.Fatalf("%+v %v", got, err)
		}
		if _, err := repo.GetStaffByPhone(ctx, "+998900000000"); !errors.Is(err, auth.ErrNotFound) {
			t.Fatal(err)
		}

		// telefon band (boshqa email bilan ham)
		dupPhone := auth.Staff{ID: uuid.New(), Email: ptr("new@izgo.uz"), Phone: op1.Phone, FullName: "X", Role: "manager", Active: true}
		if err := repo.CreateStaff(ctx, dupPhone); !errors.Is(err, auth.ErrStaffPhoneTaken) {
			t.Fatalf("telefon band: %v", err)
		}
		// email band (katta-kichik harfsiz)
		dupEmail := auth.Staff{ID: uuid.New(), Email: ptr("Boss@IZGO.uz"), Phone: "+998907770003", FullName: "X", Role: "manager", Active: true}
		if err := repo.CreateStaff(ctx, dupEmail); !errors.Is(err, auth.ErrStaffEmailTaken) {
			t.Fatalf("email band: %v", err)
		}
		// email'siz faqat operator (CHECK staff_users_email_required)
		noEmail := auth.Staff{ID: uuid.New(), Phone: "+998907770004", FullName: "X", Role: "manager", Active: true}
		if err := repo.CreateStaff(ctx, noEmail); err == nil || errors.Is(err, auth.ErrStaffPhoneTaken) || errors.Is(err, auth.ErrStaffEmailTaken) {
			t.Fatalf("email'siz manager CHECK'dan o'tib ketdi: %v", err)
		}
		// EnsureStaff band telefon bilan jim o'tkazib yuboradi (bootstrap idempotent)
		if err := repo.EnsureStaff(ctx, dupPhone); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("UpdateStaff: tranzaksiya, unikal xatolar, CHECK", func(t *testing.T) {
		a := auth.Staff{ID: uuid.New(), Email: ptr("upd-a@izgo.uz"), Phone: "+998907780001", FullName: "A", Role: "manager", Active: true}
		b := auth.Staff{ID: uuid.New(), Email: ptr("upd-b@izgo.uz"), Phone: "+998907780002", FullName: "B", Role: "manager", Active: true}
		for _, s := range []auth.Staff{a, b} {
			if err := repo.CreateStaff(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		got, err := repo.UpdateStaff(ctx, a.ID, func(s *auth.Staff) error {
			s.FullName, s.Role, s.Email, s.Active = "A2", "operator", nil, false
			return nil
		})
		if err != nil || got.FullName != "A2" || got.Email != nil || got.Active {
			t.Fatalf("%+v %v", got, err)
		}
		if got, err = repo.GetStaff(ctx, a.ID); err != nil || got.Role != "operator" || got.Email != nil || got.Active {
			t.Fatalf("saqlangani: %+v %v", got, err)
		}
		if _, err := repo.UpdateStaff(ctx, a.ID, func(s *auth.Staff) error { s.Phone = b.Phone; return nil }); !errors.Is(err, auth.ErrStaffPhoneTaken) {
			t.Fatalf("telefon band: %v", err)
		}
		if _, err := repo.UpdateStaff(ctx, a.ID, func(s *auth.Staff) error { s.Email = ptr("UPD-B@izgo.uz"); return nil }); !errors.Is(err, auth.ErrStaffEmailTaken) {
			t.Fatalf("email band: %v", err)
		}
		// email'siz manager — DB CHECK
		if _, err := repo.UpdateStaff(ctx, a.ID, func(s *auth.Staff) error { s.Role = "manager"; return nil }); err == nil {
			t.Fatal("email'siz manager CHECK'dan o'tdi")
		}
		// apply xatosi — hech narsa yozilmaydi
		stop := errors.New("stop")
		if _, err := repo.UpdateStaff(ctx, b.ID, func(s *auth.Staff) error { s.FullName = "X"; return stop }); !errors.Is(err, stop) {
			t.Fatal(err)
		}
		if got, _ := repo.GetStaff(ctx, b.ID); got.FullName != "B" {
			t.Fatalf("apply xatosida yozildi: %+v", got)
		}
		if _, err := repo.UpdateStaff(ctx, uuid.New(), func(*auth.Staff) error { return nil }); !errors.Is(err, auth.ErrNotFound) {
			t.Fatal(err)
		}
	})

	t.Run("purge", func(t *testing.T) {
		if _, err := repo.PurgeExpiredRefresh(ctx); err != nil {
			t.Fatal(err)
		}
	})
}
