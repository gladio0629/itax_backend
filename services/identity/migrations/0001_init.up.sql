-- identity_db: foydalanuvchilar (yo'lovchi/haydovchi), xodimlar, refresh tokenlar, hodisalar outbox'i.
-- Barcha ID lar UUID (ketma-ket butun ID yo'q).

CREATE TABLE users (
    id          uuid PRIMARY KEY,
    phone       text        NOT NULL CHECK (phone ~ '^\+998[0-9]{9}$'),
    role        text        NOT NULL CHECK (role IN ('passenger', 'driver')),
    first_name  text        CHECK (first_name IS NULL OR char_length(first_name) BETWEEN 1 AND 50),
    last_name   text        CHECK (last_name  IS NULL OR char_length(last_name)  BETWEEN 1 AND 50),
    settings    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- rating'ni identity yozmaydi (safar tugagach boshqa servis yangilaydi); hozircha NULL
    rating      double precision,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    -- yo'lovchi va haydovchi ilovalari alohida akkaunt: bitta telefon ikkala rolda bo'lishi mumkin
    CONSTRAINT users_phone_role_key UNIQUE (phone, role)
);

CREATE TABLE staff_users (
    id            uuid PRIMARY KEY,
    email         text        NOT NULL,
    phone         text        NOT NULL CHECK (phone ~ '^\+998[0-9]{9}$'),
    full_name     text        NOT NULL DEFAULT '',
    role          text        NOT NULL CHECK (role IN ('superadmin', 'owner', 'manager', 'marketer', 'operator')),
    is_active     boolean     NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);
CREATE UNIQUE INDEX staff_users_email_key ON staff_users (lower(email));

-- Refresh tokenlar bir martalik: used_at to'ldirilgach qayta ishlatib bo'lmaydi.
-- family_id — bitta kirish sessiyasi (token.sid); rotation shu oila ichida davom etadi.
CREATE TABLE refresh_tokens (
    jti        uuid PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id  uuid        NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_user_idx    ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_idx  ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_expires_idx ON refresh_tokens (expires_at);

-- Transactional outbox: hodisa foydalanuvchi bilan bir tranzaksiyada yoziladi, so'ng NATS'ga yuboriladi.
CREATE TABLE events_outbox (
    id           uuid PRIMARY KEY,        -- = event_id (Nats-Msg-Id)
    subject      text        NOT NULL,
    payload      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX events_outbox_pending_idx ON events_outbox (created_at) WHERE published_at IS NULL;
