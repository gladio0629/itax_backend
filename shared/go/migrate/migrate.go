// Package migrate — Go servislar (identity, trip-order, pricing) uchun umumiy SQL migratsiya qo'llovchi.
//
// Migratsiyalar servis binary'siga embed qilinadi (`migrations.FS`): `NNNN_nom.up.sql` fayllar nom
// bo'yicha tartib bilan, har biri alohida tranzaksiyada qo'llanadi va `schema_migrations` ga yoziladi.
// Qo'llangan versiya qayta bajarilmaydi, shuning uchun mavjud fayl tahrirlanmaydi — yangi fayl qo'shiladi.
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run — barcha qo'llanmagan migratsiyalarni bajaradi. lockID — servisga xos advisory lock kaliti:
// bir nechta replika bir vaqtda migratsiya qilmasin (har servis o'z qiymatini beradi).
func Run(ctx context.Context, pool *pgxpool.Pool, files fs.FS, lockID int64) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: ulanish: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return fmt.Errorf("migrate: lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lockID) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("migrate: schema_migrations: %w", err)
	}

	names, err := fs.Glob(files, "*.up.sql")
	if err != nil {
		return fmt.Errorf("migrate: fayllar: %w", err)
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, ".up.sql")
		var done bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
			return fmt.Errorf("migrate: %s: %w", version, err)
		}
		if done {
			continue
		}
		sql, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("migrate: %s: %w", version, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migrate: %s: %w", version, err)
		}
		if err := applyTx(ctx, tx, version, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrate: %s commit: %w", version, err)
		}
	}
	return nil
}

func applyTx(ctx context.Context, tx pgx.Tx, version, sql string) error {
	// Ko'p buyruqli skript: oddiy protokol (QueryExecModeSimpleProtocol) bilan bir martada yuboriladi.
	if _, err := tx.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("migrate: %s: %w", version, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("migrate: %s: %w", version, err)
	}
	return nil
}
