// Package migrations — SQL migratsiyalar binary ichiga joylanadi (scratch/distroless image'da fayl tizimi yo'q).
package migrations

import "embed"

//go:embed *.up.sql
var FS embed.FS

// LockID — shu servis migratsiyalari uchun PostgreSQL advisory lock kaliti (shared/go/migrate.Run).
// Servislar bo'yicha noyob: identity 7_310_001, trip-order 7_310_002, pricing 7_310_003.
const LockID = 7_310_001
