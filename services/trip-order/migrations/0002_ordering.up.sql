-- Buyurtma berish uchun: shaharlar katalogi, manzillar (gazetteer), saqlangan manzillar, mashhur yo'nalishlar,
-- narx taklifi (quote) va buyurtmaning yangi maydonlari (o'rindiqlar, vaqt oralig'i, narx tarkibi).
-- Shaharlar — ma'lumotnoma (har muhitda kerak), shuning uchun migratsiyada. ID lar barqaror: UUIDv5("izgo:city:<slug>").

CREATE TABLE cities (
    id         uuid PRIMARY KEY,
    slug       text        NOT NULL UNIQUE,
    name       text        NOT NULL,
    region     text        NOT NULL,
    lat        double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lng        double precision NOT NULL CHECK (lng BETWEEN -180 AND 180),
    sort       integer     NOT NULL DEFAULT 0,
    active     boolean     NOT NULL DEFAULT true
);

INSERT INTO cities (id, slug, name, region, lat, lng, sort) VALUES
    ('0a6463ca-a4e0-543c-b0a1-1c12ab814615', 'toshkent', 'Toshkent', 'Toshkent shahri', 41.2995, 69.2401, 0),
    ('2e0c3b29-70de-597c-b40e-6bdb5c9b4a2b', 'samarqand', 'Samarqand', 'Samarqand viloyati', 39.6542, 66.9597, 10),
    ('859cb1f3-9880-5d1d-9dd8-9ad29025e3cd', 'buxoro', 'Buxoro', 'Buxoro viloyati', 39.7747, 64.4286, 20),
    ('49f2c951-76e3-54d1-8dae-f44fb95c40fb', 'andijon', 'Andijon', 'Andijon viloyati', 40.7821, 72.3442, 30),
    ('589ad712-a655-5ba8-a607-d7227f711a73', 'namangan', 'Namangan', 'Namangan viloyati', 40.9983, 71.6726, 40),
    ('ecef60a4-44f8-5e17-b843-a26975a45a4d', 'fargona', 'Farg''ona', 'Farg''ona viloyati', 40.3864, 71.7864, 50),
    ('71fc8088-e689-5721-8a43-1462f25d0a7d', 'qoqon', 'Qo''qon', 'Farg''ona viloyati', 40.5286, 70.9425, 60),
    ('e94cda3c-e793-561c-a230-929b1ea44a3d', 'margilon', 'Marg''ilon', 'Farg''ona viloyati', 40.4711, 71.7247, 70),
    ('c1ef35c1-2700-582f-a271-90860e171a6a', 'navoiy', 'Navoiy', 'Navoiy viloyati', 40.0844, 65.3792, 80),
    ('269f990e-1b98-51b7-952a-5496d7cdf3cf', 'zarafshon', 'Zarafshon', 'Navoiy viloyati', 41.5667, 64.2, 90),
    ('115821c9-cb74-5340-9af6-26afcaf83274', 'qarshi', 'Qarshi', 'Qashqadaryo viloyati', 38.8606, 65.7891, 100),
    ('d6cb4bae-3864-5f59-b640-05c9da1f9f21', 'shahrisabz', 'Shahrisabz', 'Qashqadaryo viloyati', 39.0578, 66.8342, 110),
    ('bfe77dc8-fd4e-5f8c-a54c-5adf784b6e18', 'termiz', 'Termiz', 'Surxondaryo viloyati', 37.2242, 67.2783, 120),
    ('ffe038fb-9a99-5692-9727-8a8baaf408b2', 'denov', 'Denov', 'Surxondaryo viloyati', 38.2667, 67.9, 130),
    ('f231060e-c69b-5990-999d-ed08ca626dfe', 'jizzax', 'Jizzax', 'Jizzax viloyati', 40.1158, 67.8422, 140),
    ('3f2691ac-2fca-5ee8-8629-35f74485cac3', 'guliston', 'Guliston', 'Sirdaryo viloyati', 40.4897, 68.7842, 150),
    ('bfb0310f-1f2a-5965-acb9-e084c0cef27c', 'urganch', 'Urganch', 'Xorazm viloyati', 41.55, 60.6333, 160),
    ('c02b4747-f8e0-557a-8a6e-3842c5947153', 'xiva', 'Xiva', 'Xorazm viloyati', 41.3783, 60.3639, 170),
    ('0c649d0d-828f-58f6-af63-1a863c770859', 'nukus', 'Nukus', 'Qoraqalpog''iston Respublikasi', 42.46, 59.61, 180),
    ('f1ff04ec-8182-5ecd-90b5-02f2ec4e1d4e', 'chirchiq', 'Chirchiq', 'Toshkent viloyati', 41.4689, 69.5822, 190),
    ('828a2336-97a5-51e6-9b64-7abeb6bffb17', 'olmaliq', 'Olmaliq', 'Toshkent viloyati', 40.8447, 69.5983, 200),
    ('dbe5dabf-47ce-53ec-a9d3-e5d1c2c8f6f5', 'angren', 'Angren', 'Toshkent viloyati', 41.0167, 70.1436, 210),
    ('8a7f7539-1cc7-5e94-a719-c992d1bd04ea', 'kattaqorgon', 'Kattaqo''rg''on', 'Samarqand viloyati', 39.8989, 66.2561, 220);

-- Manzillar (local gazetteer): qidiruv shu jadval va shaharlar bo'yicha (tashqi geocoder kaliti klientga chiqmaydi).
-- search_key — kichik harf, apostroflarsiz (Farg'ona -> fargona).
CREATE TABLE places (
    id         uuid PRIMARY KEY,
    city_id    uuid        NOT NULL REFERENCES cities (id),
    name       text        NOT NULL,
    area       text        NOT NULL DEFAULT '',
    kind       text        NOT NULL CHECK (kind IN ('landmark', 'station', 'bus_station', 'airport', 'market', 'district', 'address')),
    lat        double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lng        double precision NOT NULL CHECK (lng BETWEEN -180 AND 180),
    entrance   text,
    search_key text        NOT NULL,
    sort       integer     NOT NULL DEFAULT 0
);
CREATE INDEX places_city_idx ON places (city_id);

CREATE TABLE saved_places (
    id         uuid PRIMARY KEY,
    user_id    uuid        NOT NULL,
    name       text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    area       text        NOT NULL DEFAULT '' CHECK (char_length(area) <= 200),
    lat        double precision NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lng        double precision NOT NULL CHECK (lng BETWEEN -180 AND 180),
    details    jsonb,
    created_at timestamptz NOT NULL
);
CREATE INDEX saved_places_user_idx ON saved_places (user_id, created_at);

CREATE TABLE popular_routes (
    id           uuid PRIMARY KEY,
    from_city_id uuid    NOT NULL REFERENCES cities (id),
    to_city_id   uuid    NOT NULL REFERENCES cities (id),
    sort         integer NOT NULL DEFAULT 0,
    active       boolean NOT NULL DEFAULT true,
    UNIQUE (from_city_id, to_city_id)
);

INSERT INTO popular_routes (id, from_city_id, to_city_id, sort) VALUES
    ('93a6cc67-09ee-5e4b-8983-ebc1237ea23f', '0a6463ca-a4e0-543c-b0a1-1c12ab814615', '2e0c3b29-70de-597c-b40e-6bdb5c9b4a2b', 0),
    ('08974667-8f9a-5292-bd37-49a0f5a23bfe', '0a6463ca-a4e0-543c-b0a1-1c12ab814615', '859cb1f3-9880-5d1d-9dd8-9ad29025e3cd', 10),
    ('5f926632-5003-5464-8923-1ad81b7992c8', '0a6463ca-a4e0-543c-b0a1-1c12ab814615', 'ecef60a4-44f8-5e17-b843-a26975a45a4d', 20),
    ('ff603d0c-6cfc-559b-9b00-b3231f5a2878', '0a6463ca-a4e0-543c-b0a1-1c12ab814615', '49f2c951-76e3-54d1-8dae-f44fb95c40fb', 30),
    ('75113b72-1222-50b3-9d31-27f71531bcc9', '0a6463ca-a4e0-543c-b0a1-1c12ab814615', '589ad712-a655-5ba8-a607-d7227f711a73', 40),
    ('b91d9cf5-d45a-5d3a-87f5-f5dc3faf3572', '0a6463ca-a4e0-543c-b0a1-1c12ab814615', '115821c9-cb74-5340-9af6-26afcaf83274', 50),
    ('e13929fd-ca23-5d7e-a7ff-42d70fc518fa', '0a6463ca-a4e0-543c-b0a1-1c12ab814615', 'f231060e-c69b-5990-999d-ed08ca626dfe', 60),
    ('540073ea-d5e8-5450-ad6a-91ad0de0a50b', '0a6463ca-a4e0-543c-b0a1-1c12ab814615', 'bfe77dc8-fd4e-5f8c-a54c-5adf784b6e18', 70),
    ('44b6e48e-cdce-545d-997e-c12b13eae73e', '2e0c3b29-70de-597c-b40e-6bdb5c9b4a2b', '859cb1f3-9880-5d1d-9dd8-9ad29025e3cd', 80),
    ('38398b02-9c69-506c-b334-9809f8799057', '0a6463ca-a4e0-543c-b0a1-1c12ab814615', 'bfb0310f-1f2a-5965-acb9-e084c0cef27c', 90);

-- Narx taklifi: buyurtma shu narx bilan yaratiladi (quote_id). input_hash — narxga ta'sir qiladigan maydonlar
-- (tur, o'rindiqlar, sinf, yuk mashinasi, qayerdan/qayerga) xeshi. Bir marta ishlatiladi.
CREATE TABLE quotes (
    id             uuid PRIMARY KEY,
    passenger_id   uuid        NOT NULL,
    input_hash     text        NOT NULL,
    price          bigint      NOT NULL CHECK (price >= 0),
    commission     bigint      NOT NULL CHECK (commission >= 0),
    distance_m     integer     NOT NULL CHECK (distance_m >= 0),
    duration_s     integer     NOT NULL CHECK (duration_s >= 0),
    lines          jsonb       NOT NULL,
    expires_at     timestamptz NOT NULL,
    created_at     timestamptz NOT NULL,
    used_order_id  uuid
);
CREATE INDEX quotes_expires_idx ON quotes (expires_at);

ALTER TABLE orders
    ADD COLUMN seat_ids       text[] CHECK (seat_ids IS NULL OR (cardinality(seat_ids) BETWEEN 1 AND 4
                                AND seat_ids <@ ARRAY['front', 'back-left', 'back-mid', 'back-right'])),
    ADD COLUMN scheduled_from timestamptz,
    ADD COLUMN scheduled_to   timestamptz,
    ADD COLUMN quote_id       uuid,
    ADD COLUMN price_lines    jsonb,
    ADD COLUMN from_city_id   uuid REFERENCES cities (id),
    ADD COLUMN to_city_id     uuid REFERENCES cities (id),
    ADD CONSTRAINT orders_schedule CHECK (
        (scheduled_from IS NULL AND scheduled_to IS NULL)
        OR (scheduled_at IS NULL AND scheduled_from IS NOT NULL AND scheduled_to IS NOT NULL AND scheduled_from < scheduled_to)
    ),
    ADD CONSTRAINT orders_seat_ids CHECK (seat_ids IS NULL OR seats = cardinality(seat_ids));
CREATE UNIQUE INDEX orders_quote_idx ON orders (quote_id) WHERE quote_id IS NOT NULL;
