import { Column, CreateDateColumn, Entity, Index, PrimaryColumn } from 'typeorm';

/** Transactional outbox: holat o'zgarishi bilan bir tranzaksiyada yoziladi, keyin NATS ga yuboriladi. */
@Entity('outbox_events')
export class OutboxEvent {
  @PrimaryColumn('uuid')
  id!: string;

  @Column('varchar', { length: 100 })
  subject!: string;

  /** To'liq envelope (shared/events/*.json) */
  @Column('jsonb')
  payload!: Record<string, unknown>;

  @CreateDateColumn({ name: 'created_at', type: 'timestamptz' })
  createdAt!: Date;

  @Index()
  @Column('timestamptz', { name: 'published_at', nullable: true })
  publishedAt!: Date | null;
}
