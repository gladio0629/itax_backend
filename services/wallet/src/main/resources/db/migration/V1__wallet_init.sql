-- Wallet servisi (wallet_db).
-- ASOSIY QOIDA: ledger_entries FAQAT INSERT. UPDATE/DELETE/TRUNCATE trigger bilan bloklanadi.
-- Barcha pul summalari tiyinda (BIGINT), ishorali: + kirim, - chiqim.

CREATE TABLE wallets (
    id         UUID PRIMARY KEY,
    driver_id  UUID        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ledger_entries (
    seq           BIGSERIAL PRIMARY KEY,            -- tartib va cursor uchun
    id            UUID        NOT NULL UNIQUE,
    wallet_id     UUID        NOT NULL REFERENCES wallets (id),
    kind          TEXT        NOT NULL CHECK (kind IN ('trip_payment', 'commission', 'topup', 'correction', 'bonus')),
    amount        BIGINT      NOT NULL CHECK (amount <> 0),
    balance_after BIGINT      NOT NULL,              -- shu yozuvdan keyingi balans (SUM natijasi)
    reference_id  UUID,                              -- trip / payment / correction ID
    reason        TEXT,                              -- correction uchun majburiy (servis darajasida)
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ledger_correction_reason CHECK (kind <> 'correction' OR (reason IS NOT NULL AND length(reason) >= 5))
);

CREATE INDEX ledger_entries_wallet_seq_idx ON ledger_entries (wallet_id, seq DESC);

-- Qayta yuborish (NATS redelivery, Backoffice retry, callback takrori) ikki marta yozmasligi uchun:
-- bitta (kind, reference_id) faqat bir marta. bonus keyinroq (bir reference ga bir nechta bo'lishi mumkin) — chiqarilgan.
CREATE UNIQUE INDEX ledger_entries_kind_ref_uniq
    ON ledger_entries (kind, reference_id)
    WHERE reference_id IS NOT NULL AND kind IN ('trip_payment', 'commission', 'topup', 'correction');

CREATE FUNCTION ledger_entries_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only: % is forbidden', TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ledger_entries_no_update_delete
    BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION ledger_entries_immutable();

CREATE TRIGGER ledger_entries_no_truncate
    BEFORE TRUNCATE ON ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_entries_immutable();

-- To'lov tranzaksiyalari (holat pending -> paid|failed o'zgaradi; bu daftar emas, UPDATE ruxsat).
CREATE TABLE payment_transactions (
    id              UUID PRIMARY KEY,
    wallet_id       UUID        NOT NULL REFERENCES wallets (id),
    user_id         UUID        NOT NULL,
    provider        TEXT        NOT NULL CHECK (provider IN ('payme', 'click', 'mock')),
    amount          BIGINT      NOT NULL CHECK (amount > 0),
    status          TEXT        NOT NULL CHECK (status IN ('pending', 'paid', 'failed')),
    idempotency_key TEXT        NOT NULL,
    request_hash    TEXT        NOT NULL,
    provider_tx_id  TEXT,
    redirect_url    TEXT        NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at         TIMESTAMPTZ,
    UNIQUE (user_id, idempotency_key)
);

-- Provayder callbacklari: (provider, Idempotency-Key) bo'yicha takroriy callback rad etiladi.
CREATE TABLE callback_receipts (
    provider        TEXT        NOT NULL,
    idempotency_key TEXT        NOT NULL,
    payment_id      UUID        NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, idempotency_key)
);

-- F-15: karta raqami (PAN) BIZGA TUSHMAYDI va saqlanmaydi. Faqat provayder tokeni va oxirgi 4 raqam.
CREATE TABLE cards (
    id         UUID PRIMARY KEY,
    wallet_id  UUID        NOT NULL REFERENCES wallets (id),
    user_id    UUID        NOT NULL,
    provider   TEXT        NOT NULL CHECK (provider IN ('payme', 'click', 'mock')),
    status     TEXT        NOT NULL CHECK (status IN ('pending', 'active', 'failed')),
    token      TEXT,
    last4      CHAR(4)     CHECK (last4 ~ '^[0-9]{4}$'),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT cards_active_has_token CHECK (status <> 'active' OR (token IS NOT NULL AND last4 IS NOT NULL))
);

-- Transactional outbox: hodisa daftar yozuvi bilan BIR tranzaksiyada yoziladi, keyin NATS ga yuboriladi.
CREATE TABLE outbox (
    id           UUID PRIMARY KEY,                   -- = event_id = Nats-Msg-Id
    subject      TEXT        NOT NULL,
    payload      TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);
CREATE INDEX outbox_unpublished_idx ON outbox (created_at) WHERE published_at IS NULL;
