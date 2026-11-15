# Local development and testing

Everything here is for a **local** stack only. Dev mode refuses to start in production (see "Safety").

## 1. Start the backend

Needs Docker with Compose v2, `openssl`, `curl` and `python3`.

```bash
cp .env.example .env          # first time only; fill POSTGRES_USER and POSTGRES_PASSWORD
./scripts/up.sh               # generates JWT keys + INTERNAL_TOKENS / UPLOAD_SIGNING_SECRET if missing, builds, starts
docker compose ps             # wait until every service is "healthy" (first build takes a few minutes)
./scripts/smoke.sh            # optional: OTP -> token -> profile -> order -> driver GPS through Kong
```

The API is at `http://localhost:8000` (Kong). Point the frontends at it with `VITE_API_URL=http://localhost:8000`.
Kong already allows CORS from `http://localhost:5173`, `5174`, `5175` and `4173` (Vite dev and preview ports).

Stop with `./scripts/down.sh` (data is kept). `docker compose down -v` wipes the databases.

## 2. Turn on dev mode

Add to `.env` (never commit `.env`):

```dotenv
APP_ENV=development
DEV_MODE=true
DEV_OTP_CODE=123456
DEV_TEST_PHONES=+998900000001,+998900000002,+998900000010,+998900000011,+998900000012,+998900000013,+998900000014
DEV_TEST_EMAILS=superadmin@izgo.test,owner@izgo.test,manager@izgo.test,marketer@izgo.test,operator@izgo.test
WS_ALLOWED_ORIGINS=localhost:*,127.0.0.1:*
```

Then recreate identity: `docker compose up -d identity`.

With `DEV_MODE=true`, every phone in `DEV_TEST_PHONES` and every email in `DEV_TEST_EMAILS` gets `DEV_OTP_CODE`
instead of a random code. That covers the passenger/driver SMS code, the admin email code and the admin SMS code.
Any other number still gets a random code. Rate limits, attempts and expiry work as usual.

## 3. Seed test data

```bash
./scripts/seed-dev.sh
```

It runs through the real APIs, so IDs match across all databases. It is safe to run again (existing records are
skipped). Each run sends OTP requests, and identity allows 5 per phone per hour (one per 60 s; the script waits out
the 60 s). It creates:

- **Reference data.** Cities (23), popular routes and the tariff come from migrations; the script only checks them.
  It also adds a few dev landmarks for address search (Amir Temur xiyoboni, Chorsu bozori, the airport, stations,
  Registon, Labi Hovuz).
- **One passenger** with a name.
- **One approved driver.** It registers (steps 1–4 with uploaded document photos), then waits for approval. With
  `AUTO_APPROVE_ENABLED=true` (the compose default) that takes 5 s; otherwise the script approves the application
  through the operator queue. It finishes by sending one GPS point in central Tashkent.
- **One staff member per role**, created through `POST /admin/v1/staff` (the superadmin through
  `scripts/seed-superadmin.sh`).

## 4. Test accounts

All codes are `DEV_OTP_CODE` (`123456` in the example above).

| Who | App | Login | Phone | Email |
|---|---|---|---|---|
| Passenger "Test Yo'lovchi" | client | phone + SMS code | `+998900000001` | — |
| Driver "Test Haydovchi" (active, Chevrolet Cobalt, economy, 4 seats) | driver | phone + SMS code | `+998900000002` | — |
| superadmin | admin | email → email code → phone → SMS code | `+998900000010` | `superadmin@izgo.test` |
| owner | admin | same | `+998900000011` | `owner@izgo.test` |
| manager (menejer) | admin | same | `+998900000012` | `manager@izgo.test` |
| marketer (marketolog) | admin | same | `+998900000013` | `marketer@izgo.test` |
| operator | admin | same | `+998900000014` | `operator@izgo.test` |

In the admin login the same code is entered twice: once for the email step and once for the SMS step. The role ids in
the API are `manager` and `marketer`; the UI shows them as menejer and marketolog.

## 5. Getting a code without dev mode

SMS and email are mocks (`SMS_MODE=mock`, `EMAIL_MODE=mock`). The code is written to the identity log:

```bash
docker compose logs identity | grep code
```

## 6. Making the driver receive offers

Offers go to drivers that matching finds near the pickup point: an `active` driver with a fresh GPS point. A point
lives `LOCATION_TTL` (2 min), so keep the driver app's request feed open (it sends GPS every 10 s), or resend:

```bash
curl -s -X POST localhost:8000/api/v1/location/update -H "Authorization: Bearer $DRIVER_TOKEN" \
  -H 'Content-Type: application/json' -d '{"lat":41.3111,"lng":69.2797}'
```

## 7. Quick API walk-through

```bash
K=http://localhost:8000; C=123456
curl -s -X POST $K/api/v1/auth/otp/request -H 'Content-Type: application/json' -d '{"phone":"+998900000001"}'
P=$(curl -s -X POST $K/api/v1/auth/otp/verify -H 'Content-Type: application/json' -H 'X-App: client' \
  -d "{\"phone\":\"+998900000001\",\"code\":\"$C\"}" | python3 -c 'import json,sys;print(json.load(sys.stdin)["access_token"])')
curl -s $K/api/v1/cities | head -c 300
curl -s "$K/api/v1/places/search?q=chorsu" -H "Authorization: Bearer $P"
BODY='{"type":"passenger","seat_ids":["front"],"from":{"lat":41.3111,"lng":69.2797},"to":{"lat":39.6548,"lng":66.9757}}'
Q=$(curl -s -X POST $K/api/v1/orders/quote -H "Authorization: Bearer $P" -H 'Content-Type: application/json' -d "$BODY")
echo "$Q"     # quote_id, price, lines (sum == price), expires_at
QID=$(echo "$Q" | python3 -c 'import json,sys;print(json.load(sys.stdin)["quote_id"])')
curl -s -X POST $K/api/v1/orders -H "Authorization: Bearer $P" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $(python3 -c 'import uuid;print(uuid.uuid4())')" \
  -d "${BODY%\}},\"payment_method\":\"cash\",\"quote_id\":\"$QID\"}"   # price == quoted price
```

## 8. Running the tests

| Service | Command |
|---|---|
| Go (identity, trip-order, pricing, matching-geo, notification) | `cd services/<name> && go test ./...` (DB tests start an embedded Postgres) |
| shared Go | `cd shared && go test ./...` |
| driver | `cd services/driver && npm test && npm run test:e2e` |
| backoffice | `cd services/backoffice && npm test` |
| wallet | `cd services/wallet && gradle test` |

## Safety

- `DEV_MODE` defaults to `false`. Identity **refuses to start** if `DEV_MODE=true` together with
  `APP_ENV=production` (or `prod`), `SMS_MODE=real` or `EMAIL_MODE=real`.
- The fixed code applies only to allowlisted test numbers and emails. Identity never logs the code or the allowlist,
  only how many entries it has.
- `seed-dev.sh` refuses to run unless `.env` has `DEV_MODE=true`. It only talks to `localhost` (override with
  `SEED_ALLOW_REMOTE=1`, which you should not do against production).
- Production `.env`: leave `DEV_MODE`, `DEV_OTP_CODE` and `DEV_TEST_*` empty, set `APP_ENV=production`, and set
  `AUTO_APPROVE_ENABLED=false` and `MOCK_LOG_SMS_PARAMS=false`.
