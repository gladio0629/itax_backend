-- pricing_db: tariflar. Pricing servisi bu jadvalni FAQAT O'QIYDI; yozishni Backoffice amalga oshiradi
-- (ikki kishi tasdig'idan keyin yangi versiya qo'shiladi va active qilinadi).
-- Pul — tiyinda (bigint); ko'paytirgichlar — promilleda (1000 = 1.0x); barcha ID lar UUID.

CREATE TABLE tariffs (
    id                    uuid PRIMARY KEY,
    version               integer     NOT NULL UNIQUE CHECK (version > 0),
    active                boolean     NOT NULL DEFAULT false,

    base_fare             bigint      NOT NULL CHECK (base_fare BETWEEN 0 AND 1000000000000),
    per_km_rate           bigint      NOT NULL CHECK (per_km_rate BETWEEN 0 AND 1000000000000),
    seat_multiplier       integer     NOT NULL DEFAULT 1000 CHECK (seat_multiplier BETWEEN 1 AND 100000),
    -- butun mashina (car_class) omillari: kalitlar to'liq bo'lishi shart
    car_class_multipliers jsonb       NOT NULL
        CHECK (jsonb_typeof(car_class_multipliers) = 'object'
               AND car_class_multipliers ?& ARRAY['economy', 'comfort', 'business', 'minivan']),
    -- yuk mashinasi (delivery) omillari
    vehicle_multipliers   jsonb       NOT NULL
        CHECK (jsonb_typeof(vehicle_multipliers) = 'object'
               AND vehicle_multipliers ?& ARRAY['spark', 'damas', 'labo', 'isuzu', 'isuzu2', 'fura']),
    fuel_surcharge        integer     NOT NULL DEFAULT 0 CHECK (fuel_surcharge BETWEEN 0 AND 10000),

    min_fare              bigint      NOT NULL DEFAULT 0 CHECK (min_fare BETWEEN 0 AND 1000000000000),
    rounding_step         bigint      NOT NULL DEFAULT 1 CHECK (rounding_step BETWEEN 1 AND 1000000000),
    commission_bps        integer     NOT NULL DEFAULT 0 CHECK (commission_bps BETWEEN 0 AND 10000),

    created_at            timestamptz NOT NULL DEFAULT now(),
    activated_at          timestamptz
);

-- Bir vaqtda faqat bitta amaldagi tarif.
CREATE UNIQUE INDEX tariffs_one_active ON tariffs ((true)) WHERE active;
