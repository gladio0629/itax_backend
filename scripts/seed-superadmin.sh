#!/usr/bin/env bash
# Birinchi superadminni identity_db ga qo'shadi (idempotent: email mavjud bo'lsa tegmaydi).
#
#   ADMIN_BOOTSTRAP_EMAIL, ADMIN_BOOTSTRAP_PHONE  — majburiy (muhitdan yoki .env dan)
#   ADMIN_BOOTSTRAP_NAME                           — ixtiyoriy (default: Superadmin)
#
# Parol saqlanmaydi: kirish email + SMS kodlari orqali. Identity servisi kamida bir marta
# ishga tushgan bo'lishi kerak (migratsiyalar jadvallarni yaratadi). Postgres docker compose
# ichida ishlaydi deb faraziladi; boshqacha bo'lsa PSQL_CMD ni bering, masalan:
#   PSQL_CMD="psql postgres://identity_svc@localhost/identity_db" ./scripts/seed-superadmin.sh
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# .env dagi qiymatlar, muhitda allaqachon berilganlarini bosib o'tmaydi.
if [[ -f .env ]]; then
  while IFS='=' read -r key val; do
    [[ "$key" =~ ^(ADMIN_BOOTSTRAP_(EMAIL|PHONE|NAME)|POSTGRES_USER)$ ]] || continue
    val="${val%\"}"; val="${val#\"}"
    [[ -n "${!key:-}" ]] || export "$key=$val"
  done < <(grep -E '^[A-Z_]+=' .env)
fi

email="${ADMIN_BOOTSTRAP_EMAIL:-}"
phone="${ADMIN_BOOTSTRAP_PHONE:-}"
name="${ADMIN_BOOTSTRAP_NAME:-Superadmin}"

[[ -n "$email" && -n "$phone" ]] || { echo "XATO: ADMIN_BOOTSTRAP_EMAIL va ADMIN_BOOTSTRAP_PHONE berilishi kerak." >&2; exit 1; }
[[ "$email" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ && ${#email} -le 254 ]] || { echo "XATO: email formati noto'g'ri." >&2; exit 1; }
[[ "$phone" =~ ^\+998[0-9]{9}$ ]] || { echo "XATO: telefon +998XXXXXXXXX formatida bo'lishi kerak." >&2; exit 1; }
email="$(printf '%s' "$email" | tr '[:upper:]' '[:lower:]')"

if [[ -n "${PSQL_CMD:-}" ]]; then
  read -r -a psql <<<"$PSQL_CMD"
else
  : "${POSTGRES_USER:?POSTGRES_USER .env da berilishi kerak}"
  psql=(docker compose exec -T postgres psql -U "$POSTGRES_USER" -d identity_db)
fi

# Qiymatlar psql o'zgaruvchilari orqali (:'x') beriladi — SQL ga qo'lda qo'shilmaydi.
result="$("${psql[@]}" -v ON_ERROR_STOP=1 -v email="$email" -v phone="$phone" -v name="$name" -tA <<'SQL'
WITH ins AS (
  INSERT INTO staff_users (id, email, phone, full_name, role, is_active)
  VALUES (gen_random_uuid(), :'email', :'phone', :'name', 'superadmin', true)
  ON CONFLICT (lower(email)) DO NOTHING
  RETURNING 1
)
SELECT CASE WHEN EXISTS (SELECT 1 FROM ins) THEN 'created' ELSE 'exists' END;
SQL
)" || { echo "XATO: baza so'rovi bajarilmadi (identity ishga tushganmi, jadvallar bormi?)." >&2; exit 1; }

case "$result" in
  created) echo "superadmin qo'shildi: $email" ;;
  exists)  echo "bu email bilan xodim allaqachon bor: $email (o'zgartirilmadi)" ;;
  *)       echo "kutilmagan javob: $result" >&2; exit 1 ;;
esac
