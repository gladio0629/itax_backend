import { Column, Entity, Index, PrimaryColumn } from 'typeorm';

/** O'zgarmas (append-only) audit jurnali: UPDATE/DELETE/TRUNCATE ni DB trigger'i taqiqlaydi. */
@Entity('audit_log')
export class AuditLog {
  @PrimaryColumn('uuid')
  id!: string;

  /** null — tizim (masalan hodisa orqali navbatga tushish) */
  @Index()
  @Column('uuid', { name: 'actor_id', nullable: true })
  actorId!: string | null;

  @Column('varchar', { name: 'actor_role', length: 20 })
  actorRole!: string;

  @Column('varchar', { length: 60 })
  action!: string;

  @Index()
  @Column('varchar', { name: 'entity_type', length: 30 })
  entityType!: string;

  @Column('varchar', { name: 'entity_id', length: 64 })
  entityId!: string;

  @Column('jsonb', { nullable: true })
  before!: Record<string, unknown> | null;

  @Column('jsonb', { nullable: true })
  after!: Record<string, unknown> | null;

  @Column('varchar', { name: 'request_id', length: 100, nullable: true })
  requestId!: string | null;

  @Index()
  @Column('timestamptz', { name: 'created_at' })
  createdAt!: Date;
}
