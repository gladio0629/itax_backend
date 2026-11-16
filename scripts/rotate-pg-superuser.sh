#!/usr/bin/env bash
# Postgres superuser parolini aylantirish (F-16/F-25). Servislar superuser'dan foydalanmaydi — ularga ta'sir qilmaydi.
#
# 1) yangi parol secrets/postgres/POSTGRES_PASSWORD.new ga yoziladi (0600);
# 2) postgres konteyneri ICHIDA, lokal socket orqali ALTER ROLE — parol fayldan psql o'zgaruvchisiga o'qiladi
#    (buyruq qatorida ham, logda ham ko'rinmaydi);
# 3) muvaffaqiyatli bo'lsa .new -> POSTGRES_PASSWORD (atomar), so'ng fix-secret-perms.sh.
# POSTGRES_PASSWORD_FILE faqat birinchi init'da o'qiladi, shuning uchun fayl va rol paroli shu skript bilan birga o'zgaradi.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
PGU=$(grep -E '^POSTGRES_USER=' .env | cut -d= -f2-); PGU=${PGU:-izgo}
NEW=secrets/postgres/POSTGRES_PASSWORD.new
(umask 077; openssl rand -hex 24 | tr -d '\n' > "$NEW")
# konteyner (root) o'qiy olsin: fayl host egasida 0600, root uchun yetarli
docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -q -U "$PGU" -d postgres \
  --set=usr="$PGU" --set=f=/run/secrets/postgres/POSTGRES_PASSWORD.new <<'SQL'
\set pass `cat :'f'`
ALTER ROLE :"usr" WITH PASSWORD :'pass';
SQL
mv -f "$NEW" secrets/postgres/POSTGRES_PASSWORD
"$ROOT/scripts/fix-secret-perms.sh"
echo "rotate-pg-superuser: $PGU paroli almashtirildi (secrets/postgres/POSTGRES_PASSWORD)" >&2
