import { MigrationInterface, QueryRunner } from 'typeorm';

/** "Yo'nalishlarim" va "Imkoniyatlarim" jadvallari. */
export class RoutesCapabilities1794756143798 implements MigrationInterface {
  name = 'RoutesCapabilities1794756143798';

  public async up(q: QueryRunner): Promise<void> {
    await q.query(`
      -- a_city_id / b_city_id — Trip & Order shahar katalogi ID lari (boshqa servis bazasi, FK yo'q)
      CREATE TABLE driver_routes (
        id         uuid PRIMARY KEY,
        driver_id  uuid NOT NULL REFERENCES drivers(id) ON DELETE CASCADE,
        a_city_id  uuid NOT NULL,
        b_city_id  uuid NOT NULL,
        both_ways  boolean NOT NULL DEFAULT false,
        position   smallint NOT NULL,
        created_at timestamptz NOT NULL DEFAULT now(),
        CHECK (a_city_id <> b_city_id)
      );
      CREATE INDEX idx_driver_routes_driver_id ON driver_routes (driver_id);

      CREATE TABLE driver_capabilities (
        driver_id       uuid PRIMARY KEY REFERENCES drivers(id) ON DELETE CASCADE,
        child_seat      boolean NOT NULL DEFAULT false,
        child_seat_ages varchar(5)[] NOT NULL DEFAULT '{}'
                        CHECK (child_seat_ages <@ ARRAY['0-1','1-3','3-7','7-12']::varchar(5)[]),
        pets            boolean NOT NULL DEFAULT false,
        big_luggage     boolean NOT NULL DEFAULT false,
        updated_at      timestamptz NOT NULL DEFAULT now(),
        CHECK (child_seat OR cardinality(child_seat_ages) = 0)
      );

      -- Ichki statistika: davr bo'yicha yangi haydovchilar
      CREATE INDEX idx_drivers_created_at ON drivers (created_at);
    `);
  }

  public async down(q: QueryRunner): Promise<void> {
    await q.query(`
      DROP INDEX IF EXISTS idx_drivers_created_at;
      DROP TABLE IF EXISTS driver_capabilities;
      DROP TABLE IF EXISTS driver_routes;
    `);
  }
}
