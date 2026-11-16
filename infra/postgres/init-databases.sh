#!/bin/bash
# Postgres birinchi marta ishga tushganda bajariladi (/docker-entrypoint-initdb.d).
# Har servis uchun alohida baza va alohida foydalanuvchi yaratadi (F-25).
# Parollar: /run/secrets/postgres/<DB>_PASSWORD (scripts/gen-secrets.sh). Fayl bo'lmasa — <DB>_PASSWORD env.
# Ikkalasi ham bo'lmasa — XATO: servis foydalanuvchisiga superuser paroli BERILMAYDI.
# Mavjud volume uchun parollarni moslash: infra/postgres/sync-passwords.sh (scripts/up.sh chaqiradi).
set -euo pipefail
SECRETS=/run/secrets/postgres

db_password() { # $1 = identity_db -> IDENTITY_DB_PASSWORD
  local var; var="$(echo "$1" | tr '[:lower:]' '[:upper:]')_PASSWORD"
  if [[ -s "$SECRETS/$var" ]]; then cat "$SECRETS/$var"; return; fi
  if [[ -n "${!var:-}" ]]; then printf '%s' "${!var}"; return; fi
  echo "init: XATO: $var yo'q ($SECRETS/$var yoki env). Avval scripts/gen-secrets.sh" >&2
  return 1
}

create_db() {
  local db="$1" user="${1%_db}_svc" pass
  pass="$(db_password "$db")"
  echo "init: creating database ${db} (owner ${user})"
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
    --set=db="$db" --set=usr="$user" --set=pass="$pass" <<'SQL'
CREATE ROLE :"usr" LOGIN PASSWORD :'pass' NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE DATABASE :"db" OWNER :"usr";
REVOKE ALL ON DATABASE :"db" FROM PUBLIC;
SQL
}

# Boshqa servis bazalariga ulanish taqiqlangan: CONNECT faqat egasiga (REVOKE ... FROM PUBLIC).
# Standart "postgres" bazasiga ham servis foydalanuvchilari ulanmasin.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres -c 'REVOKE CONNECT ON DATABASE postgres FROM PUBLIC; REVOKE CONNECT ON DATABASE template1 FROM PUBLIC;'

for db in identity_db driver_db trip_order_db pricing_db wallet_db backoffice_db; do
  create_db "$db"
done
