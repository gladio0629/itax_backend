import { MigrationInterface, QueryRunner } from 'typeorm';

/** Shikoyatlar (backoffice xodimlari ro'yxatga oladi va hal qiladi) va ularga izohlar. */
export class Complaints1731450000002 implements MigrationInterface {
  name = 'Complaints1731450000002';

  public async up(q: QueryRunner): Promise<void> {
    await q.query(`
      CREATE TABLE complaints (
        id           uuid PRIMARY KEY,
        order_id     uuid,
        subject_type text NOT NULL CHECK (subject_type IN ('passenger','driver','order')),
        subject_id   uuid NOT NULL,
        category     text NOT NULL CHECK (category IN ('driver_behavior','passenger_behavior','payment','safety','lost_item','app_issue','other')),
        description  text NOT NULL CHECK (char_length(description) BETWEEN 1 AND 2000),
        status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open','in_progress','resolved','rejected')),
        assigned_to  uuid,
        resolution   text CHECK (resolution IS NULL OR char_length(resolution) BETWEEN 1 AND 1000),
        created_by   uuid NOT NULL,
        created_at   timestamptz NOT NULL,
        updated_at   timestamptz NOT NULL
      );
      CREATE INDEX idx_complaints_created ON complaints (created_at);
      CREATE INDEX idx_complaints_status ON complaints (status);
      CREATE INDEX idx_complaints_assigned ON complaints (assigned_to);

      CREATE TABLE complaint_notes (
        id           uuid PRIMARY KEY,
        complaint_id uuid NOT NULL REFERENCES complaints (id),
        author_id    uuid NOT NULL,
        text         text NOT NULL CHECK (char_length(text) BETWEEN 1 AND 1000),
        created_at   timestamptz NOT NULL
      );
      CREATE INDEX idx_complaint_notes_complaint ON complaint_notes (complaint_id, created_at);
    `);
  }

  public async down(q: QueryRunner): Promise<void> {
    await q.query(`
      DROP TABLE IF EXISTS complaint_notes;
      DROP TABLE IF EXISTS complaints;
    `);
  }
}
