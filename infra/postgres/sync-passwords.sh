#!/bin/bash
# Mavjud (allaqachon yaratilgan) pg_data volume uchun: servis foydalanuvchilari parolini /run/secrets/postgres dagi
# fayllarga moslaydi va F-25 cheklovlarini (NOSUPERUSER, CONNECT faqat egasiga) qayta qo'llaydi. Idempotent.
# Postgres konteyneri ICHIDA ishlaydi (scripts/up.sh):  docker compose exec -T postgres /usr/local/bin/izgo-sync-passwords.sh
# Parollar psql o'zgaruvchisi sifatida fayldan o'qiladi — buyruq qatorida ham, logda ham ko'rinmaydi.
set -euo pipefail
SECRETS=/run/secrets/postgres
for db in identity_db driver_db trip_order_db pricing_db wallet_db backoffice_db; do
  user="${db%_db}_svc"
  var="$(echo "$db" | tr '[:lower:]' '[:upper:]')_PASSWORD"
  if [[ ! -s "$SECRETS/$var" ]]; then
    echo "sync: XATO: $SECRETS/$var yo'q (scripts/gen-secrets.sh)" >&2
    exit 1
  fi
  psql -v ON_ERROR_STOP=1 -q --username "$POSTGRES_USER" --dbname postgres --set=usr="$user" --set=db="$db" \
    --set=passfile="$SECRETS/$var" <<'SQL'
\set pass `cat :'passfile'`
ALTER ROLE :"usr" WITH LOGIN PASSWORD :'pass' NOSUPERUSER NOCREATEDB NOCREATEROLE;
REVOKE ALL ON DATABASE :"db" FROM PUBLIC;
SQL
  echo "sync: $user paroli moslandi"
done
psql -v ON_ERROR_STOP=1 -q --username "$POSTGRES_USER" --dbname postgres -c 'REVOKE CONNECT ON DATABASE postgres FROM PUBLIC; REVOKE CONNECT ON DATABASE template1 FROM PUBLIC;'
