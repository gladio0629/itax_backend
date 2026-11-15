import { Column, Entity, Index, PrimaryColumn } from 'typeorm';

/** Transactional outbox: biznes o'zgarishi bilan bir tranzaksiyada yoziladi, keyin NATS ga yuboriladi. */
@Entity('outbox_events')
export class OutboxEvent {
  @PrimaryColumn('uuid')
  id!: string;

  @Column('varchar', { length: 100 })
  subject!: string;

  @Column('jsonb')
  payload!: Record<string, unknown>;

  @Column('timestamptz', { name: 'created_at' })
  createdAt!: Date;

  @Index()
  @Column('timestamptz', { name: 'published_at', nullable: true })
  publishedAt!: Date | null;
}
