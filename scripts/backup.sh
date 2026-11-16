#!/usr/bin/env bash
# IZGO backup: PostgreSQL (har baza alohida + rollar), secrets/ va haydovchi hujjatlari (driver_uploads volume).
#
#   ./scripts/backup.sh                     -> $BACKUP_DIR/<UTC vaqt>/  (default BACKUP_DIR=./backups)
#
# Hammasi SHIFRLANGAN (dump'larda shaxsiy ma'lumot, secrets/ da parollar): openssl AES-256-CBC + PBKDF2 (200k),
# kalit — $BACKUP_KEY_FILE (default ~/.config/izgo/backup.key; yo'q bo'lsa yaratiladi, 0600). KALITNI BACKUP'DAN
# ALOHIDA saqlang (masalan parol menejerida): kalitsiz backup'ni tiklab bo'lmaydi, kalit bilan birga o'g'irlansa — himoya yo'q.
# Ochiq matn diskka yozilmaydi: pg_dump | openssl ... > fayl.
# Tekshirish (tiklashni sinash, ishlab turgan bazaga tegmaydi): ./scripts/restore-check.sh
# Saqlash muddati: BACKUP_KEEP_DAYS (default 14) dan eski papkalar o'chiriladi.
# Cron (har kecha 03:15):  15 3 * * * cd /opt/izgo && ./scripts/backup.sh >> backups/backup.log 2>&1
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BACKUP_DIR=${BACKUP_DIR:-$ROOT/backups}
KEY=${BACKUP_KEY_FILE:-$HOME/.config/izgo/backup.key}
KEEP_DAYS=${BACKUP_KEEP_DAYS:-14}
PGU=$(grep -E '^POSTGRES_USER=' .env 2>/dev/null | cut -d= -f2-); PGU=${PGU:-izgo}
DBS="identity_db driver_db trip_order_db pricing_db wallet_db backoffice_db"

if [[ ! -s $KEY ]]; then
  mkdir -p "$(dirname "$KEY")"; (umask 077; openssl rand -hex 32 | tr -d '\n' > "$KEY")
  echo "backup: YANGI shifrlash kaliti yaratildi: $KEY — uni xavfsiz joyga nusxalang (backup'dan alohida)!" >&2
fi
chmod 0600 "$KEY"
enc() { openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt -pass "file:$KEY" -out "$1"; }

TS=$(date -u +%Y%m%dT%H%M%SZ)
OUT="$BACKUP_DIR/$TS"
(umask 077; mkdir -p "$OUT")
echo "backup: $OUT" >&2

# 1) PostgreSQL: rollar (parollarsiz qaytadan o'rnatiladi — parollar secrets/ da) va har baza custom formatda
docker compose exec -T postgres pg_dumpall -U "$PGU" --globals-only --no-role-passwords | enc "$OUT/globals.sql.enc"
for db in $DBS; do
  docker compose exec -T postgres pg_dump -U "$PGU" -Fc -d "$db" | enc "$OUT/$db.dump.enc"
done

# 2) secrets/ (host foydalanuvchisi guruh orqali o'qiy oladi — scripts/fix-secret-perms.sh)
tar -C "$ROOT" -cf - secrets | enc "$OUT/secrets.tar.enc"

# 3) haydovchi hujjatlari (driver_uploads volume)
vol=$(docker volume ls -q --filter label=com.docker.compose.volume=driver_uploads | head -n1)
if [[ -n $vol ]]; then
  docker run --rm -v "$vol:/v:ro" redis:7-alpine tar -C /v -cf - . | enc "$OUT/driver_uploads.tar.enc"
fi

# 4) manifest (nazorat yig'indilari) — tiklashda butunlik tekshiriladi
(cd "$OUT" && sha256sum ./*.enc > SHA256SUMS)
chmod 0600 "$OUT"/*
echo "backup: tayyor — $(du -sh "$OUT" | cut -f1), fayllar: $(ls "$OUT" | tr '\n' ' ')" >&2

# 5) eski backup'larni o'chirish (faqat <vaqt> ko'rinishidagi papkalar)
find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name '20*T*Z' -mtime +"$KEEP_DAYS" -print -exec rm -rf {} + |
  sed 's/^/backup: eski o'"'"'chirildi: /' >&2 || true
