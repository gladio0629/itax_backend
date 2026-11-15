import { MigrationInterface, QueryRunner } from 'typeorm';

export class Init1731360000000 implements MigrationInterface {
  name = 'Init1731360000000';

  public async up(q: QueryRunner): Promise<void> {
    await q.query(`
      CREATE TABLE drivers (
        id                 uuid PRIMARY KEY,
        user_id            uuid NOT NULL,
        phone              varchar(13),
        first_name         varchar(50),
        last_name          varchar(50),
        birth_date         date,
        city               varchar(50),
        avatar_document_id uuid,
        state              varchar(20) NOT NULL DEFAULT 'draft'
                           CHECK (state IN ('draft','pending_review','active','rejected','blocked')),
        state_reason       varchar(100),
        state_updated_at   timestamptz NOT NULL DEFAULT now(),
        submitted_at       timestamptz,
        rating             numeric(3,2) NOT NULL DEFAULT 0,
        trips_count        integer NOT NULL DEFAULT 0,
        created_at         timestamptz NOT NULL DEFAULT now(),
        updated_at         timestamptz NOT NULL DEFAULT now()
      );
      CREATE UNIQUE INDEX uq_drivers_user_id ON drivers (user_id);
      CREATE INDEX idx_drivers_state ON drivers (state);

      CREATE TABLE registration_steps (
        driver_id    uuid NOT NULL REFERENCES drivers(id) ON DELETE CASCADE,
        step         smallint NOT NULL CHECK (step BETWEEN 1 AND 3),
        data         jsonb NOT NULL,
        completed_at timestamptz NOT NULL DEFAULT now(),
        PRIMARY KEY (driver_id, step)
      );

      CREATE TABLE cars (
        driver_id    uuid PRIMARY KEY REFERENCES drivers(id) ON DELETE CASCADE,
        brand        varchar(50) NOT NULL,
        model        varchar(50) NOT NULL,
        year         integer NOT NULL,
        color        varchar(30) NOT NULL,
        plate_number varchar(15) NOT NULL,
        car_class    varchar(20) NOT NULL,
        seats        smallint NOT NULL,
        updated_at   timestamptz NOT NULL DEFAULT now()
      );

      CREATE TABLE documents (
        id           uuid PRIMARY KEY,
        driver_id    uuid NOT NULL REFERENCES drivers(id) ON DELETE CASCADE,
        kind         varchar(30) NOT NULL,
        content_type varchar(30) NOT NULL,
        size_bytes   integer NOT NULL,
        status       varchar(20) NOT NULL DEFAULT 'pending_upload'
                     CHECK (status IN ('pending_upload','uploaded')),
        storage_key  varchar(200),
        created_at   timestamptz NOT NULL DEFAULT now(),
        uploaded_at  timestamptz
      );
      CREATE INDEX idx_documents_driver_id ON documents (driver_id);

      CREATE TABLE outbox_events (
        id           uuid PRIMARY KEY,
        subject      varchar(100) NOT NULL,
        payload      jsonb NOT NULL,
        created_at   timestamptz NOT NULL DEFAULT now(),
        published_at timestamptz
      );
      CREATE INDEX idx_outbox_unpublished ON outbox_events (created_at) WHERE published_at IS NULL;
    `);
  }

  public async down(q: QueryRunner): Promise<void> {
    await q.query(`
      DROP TABLE IF EXISTS outbox_events;
      DROP TABLE IF EXISTS documents;
      DROP TABLE IF EXISTS cars;
      DROP TABLE IF EXISTS registration_steps;
      DROP TABLE IF EXISTS drivers;
    `);
  }
}
