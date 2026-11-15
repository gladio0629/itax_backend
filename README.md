# IZGO — taksi platformasi backend

IZGO — yo'lovchi tashish va yetkazib berish (delivery) uchun taksi platformasi. Backend mikroservislardan iborat:
Go, NestJS va Kotlin servislari Kong API gateway ortida ishlaydi. Ular PostgreSQL, Redis va NATS JetStream orqali
ma'lumot saqlaydi va hodisa almashadi. Tashqi provayderlar (SMS, Payme/Click, FCM) hozircha **mock**.

## Ishga tushirish tartibi

Talab: Docker + Docker Compose v2, `openssl`, `curl`, `python3` (smoke test uchun).

```bash
cp .env.example .env          # 1. faqat nomlar; kamida POSTGRES_USER va POSTGRES_PASSWORD ni to'ldiring
./scripts/up.sh               # 2. yetishmayotgan sirlarni yaratadi va build + up qiladi (pastga qarang)
docker compose ps             # 3. hammasi "healthy" bo'lishini kuting (birinchi build bir necha daqiqa)
./scripts/smoke.sh            # 4. Kong orqali asosiy oqim: OTP -> token -> profil -> buyurtma -> haydovchi GPS
```

`./scripts/up.sh` `docker compose up -d --build` dan oldin quyidagilarni bajaradi (bor bo'lsa tegmaydi):

- `secrets/jwt/{private,public}.pem` — identity uchun Ed25519 kalitlari (`scripts/gen-jwt-keys.sh`);
- `.env` ga `UPLOAD_SIGNING_SECRET` va `INTERNAL_TOKENS` (tasodifiy, kamida 16 belgi).

`docker compose up -d` ni to'g'ridan-to'g'ri ishlatsangiz, bularni o'zingiz bering: `INTERNAL_TOKENS` bo'sh bo'lsa
`/internal/*` beradigan servislar (identity, pricing, matching-geo va boshqalar) ishga tushmaydi.

- Birinchi superadmin: `.env` da `ADMIN_BOOTSTRAP_EMAIL` + `ADMIN_BOOTSTRAP_PHONE` (identity o'zi yaratadi) yoki `./scripts/seed-superadmin.sh`.
- OTP/SMS kodlari mock: identity ularni logga yozadi — `docker compose logs identity | grep code`.
- To'xtatish: `./scripts/down.sh` (ma'lumotlar saqlanadi). Bazani to'liq tozalash: `docker compose down -v`.
- Postgres init skript (bazalar va foydalanuvchilar) faqat **birinchi** ishga tushishda (bo'sh volume) bajariladi.

`smoke.sh` ni qayta-qayta ishga tushirsangiz, OTP limiti (telefon boshiga 5/soat) tugashi mumkin — boshqa raqam bering:
`SMOKE_PASSENGER_PHONE=+998900000201 SMOKE_DRIVER_PHONE=+998900000202 ./scripts/smoke.sh`.

## Servislar va portlar

Host'ga faqat Kong (`8000` proxy, `8001` admin) va infra portlari ochiq. Ilova servislariga tashqaridan faqat
Kong orqali, ichkaridan — `izgo` Docker tarmog'ida `http://<servis>:<port>` orqali kiriladi.

| Servis        | Til     | Port | Kong orqali (public)                                       | Baza            | `/internal/*` (faqat Docker tarmog'i) |
|---------------|---------|------|------------------------------------------------------------|-----------------|----------------------------------------|
| identity      | Go      | 8010 | `/api/v1/auth/*`, `/api/v1/profile/*`, `/admin/v1/auth/*`  | `identity_db`   | `POST /internal/identity/staff`        |
| driver        | NestJS  | 8011 | `/api/v1/drivers/*`                                        | `driver_db`     | —                                      |
| trip-order    | Go      | 8012 | `/api/v1/orders/*`, `/api/v1/trips/*`                      | `trip_order_db` | —                                      |
| matching-geo  | Go      | 8013 | `POST /api/v1/location/update` (haydovchi GPS, JWT)        | — (Redis)       | `/internal/matching/*`                 |
| pricing       | Go      | 8014 | **yo'q**                                                   | `pricing_db`    | `POST /internal/pricing/calculate`     |
| wallet        | Kotlin  | 8015 | `/api/v1/wallet/*`                                         | `wallet_db`     | `/internal/wallet/*`                   |
| notification  | Go      | 8016 | `/api/v1/notifications/*`, `/ws` (WebSocket)               | — (Redis)       | `/internal/notification/*`             |
| backoffice    | NestJS  | 8017 | `/admin/v1/*` (`/admin/v1/auth/*` dan tashqari)            | `backoffice_db` | —                                      |
| risk          | Go      | 8018 | **yo'q** (stub)                                            | —               | —                                      |
| doc-scanner   | Go      | 8019 | **yo'q** (stub)                                            | —               | —                                      |

- Kong prefiks yo'llarda eng uzun mosini tanlaydi: `/admin/v1/auth/*` → identity, qolgan `/admin/v1/*` → backoffice.
- `/internal/*` yo'llari Kong'da **yo'q** va qo'shimcha ravishda `X-Internal-Token` (`INTERNAL_TOKENS`) bilan himoyalangan.
- Barcha konteynerlarda healthcheck bor (`GET /healthz` yoki servisning `healthcheck` buyrug'i; wallet — Dockerfile ichida).

| Infra     | Port        | Izoh                                                            |
|-----------|-------------|-----------------------------------------------------------------|
| postgres  | 5432 (host: `POSTGRES_HOST_PORT`) | Bitta instans, har servisga alohida baza va user (`<nom>_svc`)  |
| redis     | 6379        | Oddiy Redis (AOF yoqilgan)                                      |
| nats      | 4222        | JetStream yoqilgan (monitoring `8222` faqat tarmoq ichida)      |
| kong      | 8000 / 8001 | Proxy / Admin API, DB-less (`gateway/kong.yml`)                 |

## Muhit o'zgaruvchilari

Qiymatlar `.env` da (repoga kirmaydi); `.env.example` da faqat nomlar. Bo'sh qoldirilganlari uchun `docker-compose.yml` dagi default ishlaydi.

| O'zgaruvchi | Kim ishlatadi | Izoh |
|---|---|---|
| `POSTGRES_USER`, `POSTGRES_PASSWORD` | postgres, barcha DB servislar | **majburiy** |
| `<BAZA>_DB_PASSWORD` (`IDENTITY_`, `DRIVER_`, `TRIP_ORDER_`, `PRICING_`, `WALLET_`, `BACKOFFICE_`) | postgres init, shu servis | ixtiyoriy; bo'sh bo'lsa `POSTGRES_PASSWORD` |
| `REDIS_URL`, `NATS_URL` | barcha servislar | default `redis://redis:6379`, `nats://nats:4222` |
| `INTERNAL_TOKENS` | identity, pricing, matching-geo, wallet, notification, backoffice (trip-order — compose'da bor, hali o'qimaydi) | `X-Internal-Token` sirlari (vergul bilan bir nechta, har biri ≥16 belgi); `up.sh` yozadi |
| `JWT_ISSUER` | identity va JWT tekshiruvchi servislar | default `izgo-identity`; kalitlar `secrets/jwt/` dan mount qilinadi |
| `ADMIN_BOOTSTRAP_EMAIL`, `ADMIN_BOOTSTRAP_PHONE`, `ADMIN_BOOTSTRAP_NAME` | identity, backoffice | birinchi superadmin (ixtiyoriy) |
| `PRICING_URL`, `MATCHING_URL`, `WALLET_URL` | trip-order | default `http://pricing:8014`, `http://matching-geo:8013`, `http://wallet:8015` — mock'lar real HTTP ga almashtirilganda |
| `IDENTITY_INTERNAL_URL` | backoffice | default `http://identity:8010` (xodim yaratish) |
| `UPLOAD_SIGNING_SECRET`, `PUBLIC_BASE_URL`, `AUTO_APPROVE_*`, `APPROVAL_SYSTEM_ACTOR_ID` | driver | hujjat yuklash imzosi (`up.sh` yozadi), demo avto-tasdiq |
| `TARIFF_CACHE_TTL`, `ROAD_FACTOR`, `AVG_SPEED_KMH` | pricing (oxirgi ikkisi matching-geo ham) | tarif keshi, masofa/vaqt mock formulasi |
| `LOCATION_TTL`, `MAX_SPEED_KMH`, `DEFAULT_RADIUS_M` | matching-geo | GPS TTL, F-11 tezlik chegarasi, qidiruv radiusi |
| `PAYMENT_MODE`, `COMMISSION_BPS` | wallet | `mock`; komissiya (1000 = 10%) |
| `INTERNAL_LOOKUP_MODE`, `INTERNAL_HTTP_TIMEOUT_MS`, `TZ_OFFSET_HOURS`, `PUBLISH_APPROVED_ON_RESOLVED` | backoffice | |
| `MOCK_LOG_SMS_PARAMS`, `WS_MAX_CONNS` | notification | |
| `SMS_PROVIDER_*`, `PAYME_*`, `CLICK_*`, `HAMKORBANK_*`, `MAPBOX_TOKEN`, `FCM_KEY` | — | real provayderlar uchun (hozir ishlatilmaydi) |

## Papka strukturasi

```
onlyback/
  docker-compose.yml     infra (postgres, redis, nats, kong) + 10 ta servis
  .env.example           faqat o'zgaruvchi NOMLARI — qiymatlar .env da (gitga kirmaydi)
  gateway/kong.yml       Kong deklarativ marshrutlari (strip_path: false)
  services/<nom>/        har bir mikroservis (main + Dockerfile)
  shared/events/         NATS hodisalari uchun JSON Schema (versiya 1, umumiy envelope)
  shared/errors/         yagona xato formati va xato kodlari katalogi
  shared/go/migrate/     Go servislar uchun umumiy SQL migratsiya kodi (identity, trip-order, pricing)
  shared/go/authn/       Identity access tokenini tekshirish (trip-order, matching-geo)
  api/openapi.yml        butun platformaning API shartnomasi (OpenAPI 3.1)
  infra/postgres/        bazalar va foydalanuvchilarni yaratuvchi init skript
  infra/nats/            NATS (JetStream) konfiguratsiyasi
  infra/vault/           keyinchalik Vault konfiguratsiyasi uchun (Etap 1 da ishlatilmaydi)
  scripts/up.sh|down.sh  ishga tushirish / to'xtatish
  scripts/smoke.sh       Kong orqali asosiy oqim tekshiruvi
```

### Pricing servisi (`services/pricing`, ichki)

- `POST /internal/pricing/calculate` (shartnoma: `services/pricing/openapi.yml`). Kong tashqariga chiqarmaydi; qo'shimcha himoya —
  `X-Internal-Token` (`INTERNAL_TOKENS`, `./scripts/up.sh` `.env` ga o'zi yozadi; vergul bilan bir nechta — almashtirish uchun).
- Formula **[OCHIQ]** (hujjatda tasdiqlanmagan), shuning uchun hammasi `pricing_db.tariffs` da: `base_fare`, `per_km_rate`, `seat_multiplier`,
  `car_class_multipliers`, `vehicle_multipliers`, `fuel_surcharge` (+ `min_fare`, `rounding_step`, `commission_bps`). Boshlang'ich qiymatlar —
  `migrations/0002_default_tariff.up.sql` (taklif). Pricing tarifni faqat o'qiydi; yangisini Backoffice yozadi (yangi `version`, `active = true`).
- Tarif Redis'da 5 daqiqa keshlanadi (`TARIFF_CACHE_TTL`); Redis ishlamasa bazadan o'qiladi. Masofa = Haversine × `ROAD_FACTOR` (1.3), davomiylik = masofa / `AVG_SPEED_KMH` (30).
- **Hali ulanmagan:** Trip & Order hozir o'zining ichki mock narxidan foydalanadi (formulasi boshqacha — natijalar mos kelmaydi).

### Matching & Geo servisi (`services/matching-geo`, faqat Redis)

- Public (Kong): `POST /api/v1/location/update` `{lat, lng}` → `204`. Haydovchi ID si tokendan (`aud=driver`), body'da emas;
  yo'lovchi tokeni → `403` (`reason: role`). Tekshiruv `shared/go/authn` (Ed25519 public kalit + `bl:sid:*` blacklist).

- Endpointlar (`services/matching-geo/openapi.yml`, `X-Internal-Token` = `INTERNAL_TOKENS`): `POST /internal/matching/location` (GEOADD, TTL 2 daqiqa),
  `/find` (GEOSEARCH, default 50 km), `/route` (mock: Haversine × 1.3 — Pricing bilan bir xil `shared/geo`), `PUT /drivers/{id}/profile` va `/status`.
- Tinglaydi: `izgo.order.accepted.v1` (haydovchi → busy), `izgo.trip.completed.v1` (→ available). Chiqaradi: `izgo.offer.created.v1`
  (`/find` da `order_id` berilganda; `offer_id = UUIDv5(order_id, driver_id)`).
- F-11: oldingi nuqtadan tezlik > 300 km/soat bo'lsa signal (WARN log, Redis stream `matching:gps-anomalies`, javobda `suspicious`).
- Driver servisi profilni `POST /internal/matching/drivers/{user_id}/profile` bilan sinxronlaydi (body: `driverId, status, seats, carClass,
  vehicleType, location`): haydovchi active bo'lganda (avto-tasdiq va `izgo.driver.approved.v1`; Matching ishlamasa hodisa qayta yuboriladi)
  va har `PUT /api/v1/drivers/me` da. ID — identity user ID (JWT `sub`), Driver'ning ichki `driver.id` si emas. `vehicleType` hozircha
  doim `null` (Driver'da yuk mashinasi turi yo'q), `location` ham `null` (GPS'ni ilova o'zi yuboradi). `MATCHING_URL` bo'sh bo'lsa o'chiq.

### Notification servisi (`services/notification`, Redis)

- Tinglaydi (NATS, chiqarmaydi): `order.created`, `offer.created`, `order.accepted`, `trip.completed`, `wallet.limit_reached`, `driver.approved`.
  Push FAQAT `{type, id}` (F-21), hozircha mock (log); real FCM/APNs/SMS kodi `internal/push` va `internal/sms` oxiridagi izohlarda.
- Foydalanuvchi API: `GET /api/v1/notifications` (cursor), `PUT /api/v1/notifications/{id}/read` (Redis, TTL 30 kun). WebSocket: `/ws?token=JWT`
  (F-19: foydalanuvchi boshiga 3 ulanish, token muddati tugasa yoki logout bo'lsa uziladi; ulanish bo'lmasa push).
- Ichki API (`X-Internal-Token`): `POST /internal/notification/sms` (`phone`, `template_key`, `params`), `POST|DELETE /internal/notification/device`.
- Hali ulanmagan: Identity SMS'ni hali o'zi logga yozadi; ilovalar qurilma tokenini ichki API orqali yubora olmaydi (Driver/Identity proksi qilishi kerak).

### Trip & Order — mock'lar

Pricing, Wallet va Matching hozircha ichki mock (`services/trip-order/internal/external`), real servislarga ulanish
faqat shu paketdagi implementatsiyani almashtiradi. Manzillar compose'da tayyor (`PRICING_URL`, `MATCHING_URL`,
`WALLET_URL`, `INTERNAL_TOKENS`), lekin trip-order ularni **hali o'qimaydi**. **Mock matching** takliflarni Redis `matching:mock:drivers`
to'plamidagi haydovchilarga yaratadi — lokal sinash uchun:

```bash
docker compose exec redis redis-cli SADD matching:mock:drivers <haydovchi-user-uuid>   # buyurtma yaratishdan OLDIN
```

### Hodisalar (`shared/events/`)

Har bir fayl bitta hodisa: `event` (nomi), `version: 1`, `event_id`, `occurred_at`, `payload`.
NATS subject: `izgo.<event>.v1` (masalan `izgo.order.created.v1`).

`user.created`, `driver.submitted`, `driver.approved`, `order.created`, `order.accepted`,
`trip.completed`, `offer.created`, `payment.paid`, `wallet.limit_reached`, `tariff.approved`.

### Driver servisi (`services/driver`, NestJS)

- Endpointlar: `api/openapi.yml` dagi **Driver** guruhi (`/api/v1/drivers/...`). Ro'yxat qadamlari OpenAPI bo'yicha:
  1 — shaxsiy, 2 — mashina, 3 — hujjatlar (`license_front`, `selfie` majburiy), 4 — tekshiruvga yuborish.
- Holatlar: `draft → pending_review → active` (+ `rejected`, `blocked`). Haydovchi holatni o'zgartira olmaydi.
- Hujjat yuklash (mock): `POST /documents/upload-url` → imzolangan `PUT .../documents/{id}/content` → `uploads/` (faqat JPEG/PNG, ≤10 MB, EXIF tozalanadi). Keyinroq S3.
- Hodisalar: `izgo.driver.submitted.v1` (outbox → JetStream `DRIVER_EVENTS`), `izgo.driver.approved.v1`;
  tinglaydi: `izgo.user.created.v1` (telefon), `izgo.driver.approved.v1` (Backoffice tasdig'i).
- DEMO: `AUTO_APPROVE_ENABLED=true` bo'lsa, ariza yuborilgach 5 soniyadan keyin avtomatik `active` (`AUTO_APPROVE_DELAY_MS`).
- `.env` da `UPLOAD_SIGNING_SECRET` (kamida 16 belgi) kerak: `./scripts/up.sh` yo'q bo'lsa o'zi yozadi; `docker compose up` ni qo'lda ishlatsangiz o'zingiz bering (bo'sh bo'lsa faqat driver ishga tushmaydi).
- Testlar: `cd services/driver && npm test && npm run test:e2e` (Postgres/NATS shart emas).

### Wallet servisi (`services/wallet`, Kotlin / Spring Boot 3 / WebFlux + R2DBC)

- Public: `GET /api/v1/wallet/balance`, `GET /api/v1/wallet/ledger`, `POST /api/v1/wallet/topup` (Idempotency-Key majburiy), `POST /api/v1/wallet/card/link`. Haydovchi JWT (Ed25519, public kalit) + `bl:sid:*` blacklist.
- Ichki (`X-Internal-Token`, Kong'da YO'Q): `POST /internal/wallet/check-limit` (`{driver_id}` -> `{allowed, balance, debt_status, limit}`), `POST /internal/wallet/payment-callback`, `POST /internal/wallet/correction` (Backoffice tasdiqlagan tuzatish; `correction_id` + `Idempotency-Key`, sabab majburiy).
- **Daftar faqat INSERT**: `ledger_entries` da UPDATE/DELETE/TRUNCATE DB trigger bilan bloklangan; balans = `SUM(amount)`; bir hamyonga yozuvlar advisory qulf bilan ketma-ket. Turlari: `trip_payment`, `commission`, `topup`, `correction`, `bonus`.
- Komissiya: `COMMISSION_BPS` (1000 = 10%, HALF_UP). Qarz chegarasi `-5 000 000` tiyin: balans `<=` chegara bo'lsa `at_limit` (yangi buyurtma yo'q, komissiya baribir yechiladi), `approaching` — chegaraning 80% idan.
- `trip.completed` (JetStream durable `wallet-trip-completed`) -> elektron to'lovda `trip_payment +narx` va `commission -10%`, naqdda faqat `commission`. `payment_method` Trip & Order tomonidan har doim yuboriladi (`shared/events/trip.completed.json` da majburiy); eski (maydonsiz) hodisa naqd deb olinadi.
- Chiqaradi (transactional outbox -> stream `WALLET_EVENTS`): `payment.paid`, `wallet.limit_reached` (faqat chegara kesib o'tilganda, bir marta).
- Payme/Click/Hamkorbank hozir **MOCK** (`PAYMENT_MODE=mock`): topup darhol `paid`. Real kod: `payment/providers/RealProviders.kt` va `payment/PaymentProvider.kt` oxirida — butunlay kommentda. F-15: karta raqami saqlanmaydi (faqat `token` + `last4`).
- Kalitlar: Redis `idem:topup:<user>:<key>`, `idem:cb:<provider>:<key>` — faqat kesh; haqiqat manbai DB unique cheklovlari.
- Testlar: `cd services/wallet && gradle test` (JUnit 5 + MockK; DB/NATS shart emas).

### Backoffice servisi (`services/backoffice`, NestJS)

- Endpointlar: `api/openapi.yml` dagi **Admin** guruhi (`/admin/v1/...`). Rollar/ruxsatlar — `shared/roles/roles.json` (image ichiga nusxalanadi; build konteksti repo ildizi).
- Admin JWT (aud=`admin`) identity public kaliti bilan tekshiriladi. Ruxsat HAR so'rovda DB dagi xodim roli bo'yicha `roles.json` dan olinadi (tokendagi `perms` ishlatilmaydi); bloklangan/arxivlangan xodim darhol rad etiladi. Barcha endpointlar default-deny.
- Ikki kishi tasdig'i: tuzatishlar (`wallet/correction`) va tariflar `pending_approval`; yaratgan xodim o'zi tasdiqlay/rad eta olmaydi.
- Audit (`audit_log`): har yozish amali biznes o'zgarishi bilan bir tranzaksiyada; jadval DB trigger'i bilan o'zgarmas (UPDATE/DELETE/TRUNCATE taqiqlangan).
- Operator navbati `izgo.driver.submitted.v1` dan to'ldiriladi; `resolved` yakunlangan haydovchi arizasi uchun `izgo.driver.approved.v1` chiqadi (`PUBLISH_APPROVED_ON_RESOLVED`).
- Xodim yaratish identity bilan HAQIQIY HTTP orqali: `POST {IDENTITY_INTERNAL_URL}/internal/identity/staff` (`X-Internal-Token` = `INTERNAL_TOKENS`). Identity xodim ID sini o'zi beradi (token `sub`), backoffice shuni ishlatadi. 409 → `STAFF_PHONE_TAKEN`/`STAFF_EMAIL_TAKEN`; identity javob bermasa → 502 `IDENTITY_UNAVAILABLE`, xodim yaratilmaydi. Tahrirlash/bloklash/arxivlash: `PUT {IDENTITY_INTERNAL_URL}/internal/identity/staff/{id}` (`{phone, email, role, name, active}`, faqat yuborilganlar o'zgaradi); identity rad etsa backoffice tranzaksiyasi bekor bo'ladi.
- Driver/user lookup (operator navbatida telefon) HAQIQIY HTTP orqali: `GET {DRIVER_INTERNAL_URL}/internal/driver/drivers/{id}`, `GET {IDENTITY_INTERNAL_URL}/internal/identity/users/{id}` → `{id, phone, firstName, lastName, role}` (compose'da `INTERNAL_LOOKUP_MODE=http`; kod defaulti va testlar — `mock`). Lookup xatosi navbatni buzmaydi — telefon `null` qaytadi.
- Driver/passenger CRUD, order, wallet, stats hozir MOCK portlar (`src/ports/`) orqali.
- Testlar: `cd services/backoffice && npm test` (pg-mem, Postgres/NATS shart emas).

### Umumiy qoidalar

- Barcha ID lar — UUID; pul — butun son, tiyinda.
- Ro'yxatlar: `{ "data": [...], "cursor": "..." | null }`.
- Narx, masofa, status, komissiya, qarz — faqat server hisoblaydi; klient yubormaydi.
- Xato: `{ "error": { "key": "STRING", "params": {} } }`.
- Maxfiy qiymatlar faqat lokal `.env` da; repoga yozilmaydi.
- Tashqi tarmoq chaqiruvlari yo'q (SMS, Payme/Click — hozircha mock).

### Ikki kishi tasdig'i

Wallet tuzatishi va tarif o'zgarishi `approval_status: pending_approval` bilan yaratiladi. Ikkinchi xodim
`POST /admin/v1/wallet/correction/{id}/approve` yoki `POST /admin/v1/tariffs/{id}/approve` bilan
tasdiqlaydi (`pending_approval` → `approved`) yoki `.../{id}/reject` bilan (sabab majburiy) rad etadi
(`pending_approval` → `rejected`). Yaratgan xodim o'zi tasdiqlay ham, rad eta ham olmaydi.

### Operator navbati

`waiting` → `start` → `in_progress` → `complete` (`result: resolved | unresolved`, `comment`) → `completed`,
yoki `reject` (sabab majburiy) → `rejected`.

## Ochiq savollar


1. Haydovchi arizasi (`kind: driver_application`) `result: resolved` bilan yakunlanganda `driver.approved`
   hodisasi chiqarilsinmi yoki haydovchini faollashtirish alohida amalmi?
