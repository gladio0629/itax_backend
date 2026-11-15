#!/bin/bash
# Postgres birinchi marta ishga tushganda bajariladi (/docker-entrypoint-initdb.d).
# Har servis uchun alohida baza va alohida foydalanuvchi yaratadi.
# Parollar fayllarda saqlanmaydi: <DB>_PASSWORD env o'zgaruvchisi berilmasa, POSTGRES_PASSWORD ishlatiladi.
set -euo pipefail

create_db() {
  local db="$1"
  local user="${db%_db}_svc"
  local pass_var
  pass_var="$(echo "${db}" | tr '[:lower:]' '[:upper:]')_PASSWORD"
  local pass="${!pass_var:-$POSTGRES_PASSWORD}"

  echo "init: creating database ${db} (owner ${user})"
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
    --set=db="$db" --set=usr="$user" --set=pass="$pass" <<'SQL'
CREATE ROLE :"usr" LOGIN PASSWORD :'pass';
CREATE DATABASE :"db" OWNER :"usr";
REVOKE ALL ON DATABASE :"db" FROM PUBLIC;
SQL
}

for db in identity_db driver_db trip_order_db pricing_db wallet_db backoffice_db; do
  create_db "$db"
done
