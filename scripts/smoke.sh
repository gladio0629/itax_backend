#!/usr/bin/env bash
# Kong orqali asosiy oqimni tekshiradi (docker compose up -d dan keyin):
#   1) barcha konteynerlar healthy
#   2) yo'lovchi: OTP so'rash -> kod (identity logidan, SMS mock) -> token
#   3) GET /api/v1/profile
#   4) POST /api/v1/orders (narx server tomonidan)
#   5) haydovchi: OTP -> token (X-App: driver) -> POST /api/v1/location/update (204)
#   6) /internal/* Kong orqali ochilmasligi (404)
# Talab: curl, python3. Har qadam muvaffaqiyatsiz bo'lsa skript to'xtaydi.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

KONG="${KONG_URL:-http://localhost:8000}"
PASSENGER_PHONE="${SMOKE_PASSENGER_PHONE:-+998900000101}"
DRIVER_PHONE="${SMOKE_DRIVER_PHONE:-+998900000102}"

ok()   { printf '  \033[32mOK\033[0m  %s\n' "$*"; }
fail() { printf '  \033[31mXATO\033[0m %b\n' "$*" >&2; exit 1; }
json() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval("d"+sys.argv[1]))' "$1"; }

# req METHOD PATH BODY [HEADER...] -> "STATUS\nBODY"
req() {
  local m=$1 p=$2 b=$3; shift 3
  local args=(-s -o /tmp/izgo-smoke.body -w '%{http_code}' -X "$m" "$KONG$p" -H 'Content-Type: application/json')
  for h in "$@"; do args+=(-H "$h"); done
  [[ -n "$b" ]] && args+=(--data "$b")
  local code; code=$(curl "${args[@]}")
  printf '%s\n%s' "$code" "$(cat /tmp/izgo-smoke.body)"
}
status() { head -n1 <<<"$1"; }
body()   { tail -n +2 <<<"$1"; }

echo "1) Konteynerlar holati"
for _ in $(seq 1 60); do
  bad=$(docker compose ps --format '{{.Service}} {{.Health}} {{.State}}' | awk '$2!="healthy" || $3!="running"' || true)
  [[ -z "$bad" ]] && break
  sleep 5
done
[[ -z "$bad" ]] || fail "healthy emas:\n$bad\n(docker compose logs <servis> bilan sababini ko'ring)"
ok "barcha servislar healthy"

# otp_login PHONE APP -> access token
otp_login() {
  local phone=$1 app=$2 since r code
  since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  r=$(req POST /api/v1/auth/otp/request "{\"phone\":\"$phone\"}")
  [[ $(status "$r") == 202 ]] || fail "otp/request ($phone): $r"
  # SMS mock: identity kodni JSON logga yozadi
  for _ in $(seq 1 10); do
    code=$(docker compose logs --no-log-prefix --since "$since" identity 2>/dev/null \
      | grep -F "\"phone\":\"$phone\"" | grep -o '"code":"[0-9]*"' | tail -n1 | grep -o '[0-9]*' || true)
    [[ -n "$code" ]] && break
    sleep 1
  done
  [[ -n "$code" ]] || fail "OTP kod identity logida topilmadi ($phone)"
  r=$(req POST /api/v1/auth/otp/verify "{\"phone\":\"$phone\",\"code\":\"$code\"}" "X-App: $app")
  [[ $(status "$r") == 200 ]] || fail "otp/verify ($phone): $r"
  body "$r" | json '["access_token"]'
}

echo "2) Yo'lovchi: OTP -> token"
PTOK=$(otp_login "$PASSENGER_PHONE" client)
ok "access token olindi"

echo "3) Profil"
r=$(req GET /api/v1/profile "" "Authorization: Bearer $PTOK")
[[ $(status "$r") == 200 ]] || fail "profile: $r"
ok "profil: $(body "$r" | json '["phone"]')"

echo "4) Buyurtma yaratish"
order='{"type":"passenger","seats":1,"payment_method":"cash",
  "from":{"lat":41.3111,"lng":69.2797,"address":"Amir Temur xiyoboni"},
  "to":{"lat":41.2995,"lng":69.2401}}'
r=$(req POST /api/v1/orders "$order" "Authorization: Bearer $PTOK" "Idempotency-Key: $(python3 -c 'import uuid;print(uuid.uuid4())')")
[[ $(status "$r") == 201 ]] || fail "orders: $r"
ok "buyurtma $(body "$r" | json '["id"]'), status=$(body "$r" | json '["status"]'), narx=$(body "$r" | json '["price"]') tiyin"

echo "5) Haydovchi: OTP -> token -> GPS"
DTOK=$(otp_login "$DRIVER_PHONE" driver)
r=$(req POST /api/v1/location/update '{"lat":41.3111,"lng":69.2797}' "Authorization: Bearer $DTOK")
[[ $(status "$r") == 204 ]] || fail "location (haydovchi): $r"
ok "haydovchi joylashuvi qabul qilindi"
r=$(req POST /api/v1/location/update '{"lat":41.3111,"lng":69.2797}' "Authorization: Bearer $PTOK")
[[ $(status "$r") == 403 ]] || fail "location (yo'lovchi tokeni 403 bo'lishi kerak edi): $r"
ok "yo'lovchi tokeni bilan location rad etildi (403)"

echo "6) /internal/* Kong orqali yopiq"
for p in /internal/pricing/calculate /internal/matching/find /internal/identity/staff /internal/wallet/check-limit /internal/notification/sms; do
  r=$(req POST "$p" '{}')
  [[ $(status "$r") == 404 ]] || fail "$p Kong orqali ochiq: $r"
done
ok "ichki yo'llar Kong'da yo'q (404)"

rm -f /tmp/izgo-smoke.body
echo "Hammasi o'tdi."
