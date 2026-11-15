import { MigrationInterface, QueryRunner } from 'typeorm';

/** audit_log o'zgarmas: UPDATE, DELETE va TRUNCATE trigger orqali taqiqlanadi. */
export class AuditImmutable1731450000001 implements MigrationInterface {
  name = 'AuditImmutable1731450000001';

  public async up(q: QueryRunner): Promise<void> {
    await q.query(`
      CREATE FUNCTION audit_log_immutable() RETURNS trigger AS $$
      BEGIN
        RAISE EXCEPTION 'audit_log o''zgarmas: % taqiqlangan', TG_OP USING ERRCODE = 'insufficient_privilege';
      END;
      $$ LANGUAGE plpgsql;
    `);
    await q.query(`
      CREATE TRIGGER audit_log_no_update_delete
        BEFORE UPDATE OR DELETE ON audit_log
        FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
    `);
    await q.query(`
      CREATE TRIGGER audit_log_no_truncate
        BEFORE TRUNCATE ON audit_log
        FOR EACH STATEMENT EXECUTE FUNCTION audit_log_immutable();
    `);
  }

  public async down(q: QueryRunner): Promise<void> {
    await q.query(`DROP TRIGGER IF EXISTS audit_log_no_truncate ON audit_log`);
    await q.query(`DROP TRIGGER IF EXISTS audit_log_no_update_delete ON audit_log`);
    await q.query(`DROP FUNCTION IF EXISTS audit_log_immutable()`);
  }
}
