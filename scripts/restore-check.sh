#!/usr/bin/env bash
# Backup'ni TIKLASHNI sinash (ishlab turgan bazaga TEGMAYDI): eng oxirgi (yoki berilgan) backup vaqtinchalik
# postgres konteyneriga tiklanadi, har bazadagi jadvallar va qatorlar soni ishlab turgan baza bilan solishtiriladi;
# secrets.tar va driver_uploads.tar ochilishi tekshiriladi.
#
#   ./scripts/restore-check.sh [backups/<vaqt>]
#
# Haqiqiy tiklash (avariya) — docs/hosting.md "Backup" bo'limi.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
KEY=${BACKUP_KEY_FILE:-$HOME/.config/izgo/backup.key}
DIR=${1:-$(ls -d "${BACKUP_DIR:-$ROOT/backups}"/20*T*Z 2>/dev/null | sort | tail -n1)}
[[ -n $DIR && -d $DIR ]] || { echo "XATO: backup topilmadi" >&2; exit 1; }
PGU=$(grep -E '^POSTGRES_USER=' .env 2>/dev/null | cut -d= -f2-); PGU=${PGU:-izgo}
DBS="identity_db driver_db trip_order_db pricing_db wallet_db backoffice_db"
dec() { openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass "file:$KEY" -in "$1"; }
echo "restore-check: $DIR" >&2
(cd "$DIR" && sha256sum -c --quiet SHA256SUMS) && echo "  nazorat yig'indilari: OK" >&2

NAME=izgo-restore-check-$$
PW=$(openssl rand -hex 16)
trap 'docker rm -f "$NAME" >/dev/null 2>&1 || true' EXIT
docker run -d --rm --name "$NAME" -e POSTGRES_USER="$PGU" -e POSTGRES_PASSWORD="$PW" --tmpfs /var/lib/postgresql/data \
  postgres:16-alpine >/dev/null
for _ in $(seq 1 60); do docker exec "$NAME" pg_isready -U "$PGU" -h 127.0.0.1 >/dev/null 2>&1 && break; sleep 1; done
sleep 2

dec "$DIR/globals.sql.enc" | docker exec -i "$NAME" psql -q -U "$PGU" -d postgres >/dev/null 2>&1 || true
count() { # konteyner baza -> "jadvallar qatorlar"
  docker exec "$1" psql -U "$PGU" -d "$2" -Atc "select count(*), coalesce(sum(n_live_tup),0) from pg_stat_user_tables" 2>/dev/null |
    tr '|' ' '
}
fail=0
for db in $DBS; do
  docker exec "$NAME" createdb -U "$PGU" "$db"
  dec "$DIR/$db.dump.enc" | docker exec -i "$NAME" pg_restore -U "$PGU" -d "$db" --no-owner --exit-on-error
  docker exec "$NAME" psql -q -U "$PGU" -d "$db" -c 'ANALYZE' >/dev/null
  docker compose exec -T postgres psql -q -U "$PGU" -d "$db" -c 'ANALYZE' >/dev/null
  got=$(docker exec "$NAME" psql -U "$PGU" -d "$db" -Atc "select count(*) from information_schema.tables where table_schema='public'")
  live=$(docker compose exec -T postgres psql -U "$PGU" -d "$db" -Atc "select count(*) from information_schema.tables where table_schema='public'")
  status=OK; [[ $got == "$live" ]] || { status="FARQ (jonli: $live)"; fail=1; }
  echo "  $db: jadvallar $got, qatorlar ~$(count "$NAME" "$db" | cut -d' ' -f2) — $status" >&2
done
dec "$DIR/secrets.tar.enc" | tar -tf - >/dev/null && echo "  secrets.tar: ochiladi ($(dec "$DIR/secrets.tar.enc" | tar -tf - | grep -vc '/$') fayl)" >&2
if [[ -f $DIR/driver_uploads.tar.enc ]]; then
  dec "$DIR/driver_uploads.tar.enc" | tar -tf - >/dev/null && echo "  driver_uploads.tar: ochiladi ($(dec "$DIR/driver_uploads.tar.enc" | tar -tf - | grep -vc '/$') fayl)" >&2
fi
[[ $fail -eq 0 ]] && echo "restore-check: OK" >&2 || { echo "restore-check: XATO" >&2; exit 1; }
