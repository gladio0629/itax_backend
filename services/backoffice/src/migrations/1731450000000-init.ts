import { MigrationInterface, QueryRunner } from 'typeorm';

export class Init1731450000000 implements MigrationInterface {
  name = 'Init1731450000000';

  public async up(q: QueryRunner): Promise<void> {
    await q.query(`
      CREATE TABLE staff (
        id         uuid PRIMARY KEY,
        email      varchar(254),
        phone      varchar(13),
        full_name  varchar(100) NOT NULL DEFAULT '',
        role       varchar(20) NOT NULL CHECK (role IN ('superadmin','owner','manager','marketer','operator')),
        state      varchar(20) NOT NULL DEFAULT 'active' CHECK (state IN ('active','blocked','archived')),
        created_by uuid,
        created_at timestamptz NOT NULL,
        updated_at timestamptz NOT NULL
      );
      CREATE UNIQUE INDEX uq_staff_email ON staff (email);
      CREATE UNIQUE INDEX uq_staff_phone ON staff (phone);

      CREATE TABLE queue_items (
        id              uuid PRIMARY KEY,
        kind            varchar(30) NOT NULL CHECK (kind IN ('driver_application','document_update','complaint')),
        subject_id      uuid NOT NULL,
        status          varchar(20) NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting','in_progress','completed','rejected')),
        assigned_to     uuid,
        finished_by     uuid,
        result          varchar(20) CHECK (result IS NULL OR result IN ('resolved','unresolved')),
        comment         varchar(1000),
        reject_reason   varchar(500),
        source_event_id uuid,
        created_at      timestamptz NOT NULL,
        finished_at     timestamptz
      );
      CREATE UNIQUE INDEX uq_queue_source_event ON queue_items (source_event_id);
      CREATE INDEX idx_queue_status ON queue_items (status);

      CREATE TABLE tariffs (
        id                 uuid PRIMARY KEY,
        name               varchar(100) NOT NULL,
        car_class          varchar(20) NOT NULL,
        base_fare          bigint NOT NULL CHECK (base_fare >= 0),
        per_km             bigint NOT NULL CHECK (per_km >= 0),
        per_min            bigint NOT NULL CHECK (per_min >= 0),
        min_fare           bigint NOT NULL CHECK (min_fare >= 0),
        commission_percent numeric(5,2) NOT NULL CHECK (commission_percent BETWEEN 0 AND 100),
        active             boolean NOT NULL DEFAULT false,
        approval_status    varchar(20) NOT NULL CHECK (approval_status IN ('pending_approval','approved','rejected')),
        pending_change     jsonb,
        deleted_at         timestamptz,
        created_at         timestamptz NOT NULL,
        updated_at         timestamptz NOT NULL
      );

      CREATE TABLE corrections (
        id              uuid PRIMARY KEY,
        wallet_id       uuid NOT NULL,
        amount          bigint NOT NULL CHECK (amount <> 0),
        reason          varchar(500) NOT NULL,
        approval_status varchar(20) NOT NULL CHECK (approval_status IN ('pending_approval','approved','rejected')),
        created_by      uuid NOT NULL,
        approved_by     uuid,
        reject_reason   varchar(500),
        idempotency_key uuid,
        request_hash    varchar(64),
        created_at      timestamptz NOT NULL,
        decided_at      timestamptz
      );
      CREATE INDEX idx_corrections_status ON corrections (approval_status);
      CREATE UNIQUE INDEX uq_corrections_idem ON corrections (created_by, idempotency_key);

      CREATE TABLE audit_log (
        id          uuid PRIMARY KEY,
        actor_id    uuid,
        actor_role  varchar(20) NOT NULL,
        action      varchar(60) NOT NULL,
        entity_type varchar(30) NOT NULL,
        entity_id   varchar(64) NOT NULL,
        before      jsonb,
        after       jsonb,
        request_id  varchar(100),
        created_at  timestamptz NOT NULL
      );
      CREATE INDEX idx_audit_actor ON audit_log (actor_id);
      CREATE INDEX idx_audit_entity ON audit_log (entity_type, entity_id);
      CREATE INDEX idx_audit_created ON audit_log (created_at);

      CREATE TABLE outbox_events (
        id           uuid PRIMARY KEY,
        subject      varchar(100) NOT NULL,
        payload      jsonb NOT NULL,
        created_at   timestamptz NOT NULL,
        published_at timestamptz
      );
      CREATE INDEX idx_outbox_published ON outbox_events (published_at);
    `);
  }

  public async down(q: QueryRunner): Promise<void> {
    await q.query(`
      DROP TABLE IF EXISTS outbox_events;
      DROP TABLE IF EXISTS audit_log;
      DROP TABLE IF EXISTS corrections;
      DROP TABLE IF EXISTS tariffs;
      DROP TABLE IF EXISTS queue_items;
      DROP TABLE IF EXISTS staff;
    `);
  }
}
