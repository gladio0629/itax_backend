-- trip_order_db: buyurtmalar, takliflar, safarlar, baholar, hodisalar outbox'i.
-- Barcha ID lar UUID. Vaqtlar timestamptz, summalar — tiyinda (bigint).

CREATE TABLE orders (
    id                uuid PRIMARY KEY,
    passenger_id      uuid        NOT NULL,
    type              text        NOT NULL CHECK (type IN ('passenger', 'delivery')),
    seats             smallint    CHECK (seats BETWEEN 1 AND 4),
    car_class         text        CHECK (car_class IN ('economy', 'comfort', 'business', 'minivan')),
    vehicle_type      text        CHECK (vehicle_type IN ('spark', 'damas', 'labo', 'isuzu', 'isuzu2', 'fura')),
    from_lat          double precision NOT NULL CHECK (from_lat BETWEEN -90 AND 90),
    from_lng          double precision NOT NULL CHECK (from_lng BETWEEN -180 AND 180),
    from_address      text,
    to_lat            double precision NOT NULL CHECK (to_lat BETWEEN -90 AND 90),
    to_lng            double precision NOT NULL CHECK (to_lng BETWEEN -180 AND 180),
    to_address        text,
    scheduled_at      timestamptz,
    payment_method    text        NOT NULL CHECK (payment_method IN ('cash', 'card', 'wallet', 'payme', 'click')),
    comment           text        CHECK (comment IS NULL OR char_length(comment) <= 200),
    status            text        NOT NULL CHECK (status IN ('searching', 'accepted', 'in_progress', 'completed', 'cancelled', 'expired')),
    price             bigint      NOT NULL CHECK (price >= 0),
    commission        bigint      NOT NULL CHECK (commission >= 0),
    distance_m        integer     NOT NULL CHECK (distance_m >= 0),
    duration_s        integer     NOT NULL CHECK (duration_s >= 0),
    driver_id         uuid,
    trip_id           uuid,
    cancel_reason     text,
    cancel_comment    text,
    idempotency_key   text,
    search_expires_at timestamptz NOT NULL,
    version           integer     NOT NULL DEFAULT 1,
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL,

    -- tur bo'yicha maydonlar: passenger -> seats YOKI car_class; delivery -> faqat vehicle_type
    CONSTRAINT orders_shape CHECK (
        (type = 'passenger' AND vehicle_type IS NULL AND ((seats IS NOT NULL)::int + (car_class IS NOT NULL)::int) = 1)
        OR
        (type = 'delivery' AND vehicle_type IS NOT NULL AND seats IS NULL AND car_class IS NULL)
    ),
    -- haydovchi biriktirilgan holatlarda driver_id va trip_id majburiy
    CONSTRAINT orders_assigned CHECK (
        status IN ('searching', 'cancelled', 'expired') OR (driver_id IS NOT NULL AND trip_id IS NOT NULL)
    )
);
-- Idempotency-Key: bir yo'lovchi uchun bir xil kalit ikkinchi buyurtma yaratmaydi
CREATE UNIQUE INDEX orders_idem_key ON orders (passenger_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX orders_history_idx ON orders (passenger_id, created_at DESC, id DESC);
CREATE INDEX orders_searching_idx ON orders (search_expires_at) WHERE status = 'searching';
CREATE INDEX orders_driver_idx ON orders (driver_id) WHERE driver_id IS NOT NULL;

CREATE TABLE offers (
    id            uuid PRIMARY KEY,
    order_id      uuid        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    driver_id     uuid        NOT NULL,
    pickup_eta_s  integer     NOT NULL CHECK (pickup_eta_s >= 0),
    status        text        NOT NULL CHECK (status IN ('pending', 'accepted', 'closed')),
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL,
    UNIQUE (order_id, driver_id)
);
CREATE INDEX offers_driver_idx ON offers (driver_id, created_at DESC, id DESC) WHERE status = 'pending';

CREATE TABLE trips (
    id           uuid PRIMARY KEY,
    order_id     uuid        NOT NULL UNIQUE REFERENCES orders (id),
    driver_id    uuid        NOT NULL,
    passenger_id uuid        NOT NULL,
    status       text        NOT NULL CHECK (status IN ('assigned', 'in_progress', 'completed', 'cancelled')),
    started_at   timestamptz,
    completed_at timestamptz,
    final_price  bigint      CHECK (final_price >= 0),
    commission   bigint      CHECK (commission >= 0),
    distance_m   integer     CHECK (distance_m >= 0),
    duration_s   integer     CHECK (duration_s >= 0),
    created_at   timestamptz NOT NULL,
    CONSTRAINT trips_completed CHECK (
        status <> 'completed' OR (completed_at IS NOT NULL AND final_price IS NOT NULL AND commission IS NOT NULL)
    )
);
CREATE INDEX trips_driver_active_idx ON trips (driver_id) WHERE status IN ('assigned', 'in_progress');

ALTER TABLE orders ADD CONSTRAINT orders_trip_fk FOREIGN KEY (trip_id) REFERENCES trips (id) DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE ratings (
    order_id           uuid PRIMARY KEY REFERENCES orders (id),
    passenger_id       uuid        NOT NULL,
    driver_id          uuid        NOT NULL,
    stars              smallint    NOT NULL CHECK (stars BETWEEN 1 AND 5),
    reason_kind        text        CHECK (reason_kind IN ('polite', 'good_talk', 'comfort', 'just_because', 'custom')),
    reason_custom_text text        CHECK (reason_custom_text IS NULL OR char_length(reason_custom_text) BETWEEN 1 AND 200),
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz,
    -- 5 yulduz -> sabab majburiy; boshqa baholarda sabab yo'q; custom_text faqat custom uchun
    CONSTRAINT ratings_reason CHECK ((stars = 5) = (reason_kind IS NOT NULL)),
    CONSTRAINT ratings_custom CHECK ((reason_kind = 'custom') = (reason_custom_text IS NOT NULL))
);
CREATE INDEX ratings_driver_idx ON ratings (driver_id);

CREATE TABLE events_outbox (
    id           uuid PRIMARY KEY,        -- = event_id (Nats-Msg-Id)
    subject      text        NOT NULL,
    payload      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL,
    published_at timestamptz
);
CREATE INDEX events_outbox_pending_idx ON events_outbox (created_at, id) WHERE published_at IS NULL;
