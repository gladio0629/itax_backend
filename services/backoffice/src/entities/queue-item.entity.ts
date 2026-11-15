import { Column, Entity, Index, PrimaryColumn } from 'typeorm';

export type QueueKind = 'driver_application' | 'document_update' | 'complaint';
export type QueueStatus = 'waiting' | 'in_progress' | 'completed' | 'rejected';
export type QueueResult = 'resolved' | 'unresolved';

@Entity('queue_items')
export class QueueItem {
  @PrimaryColumn('uuid')
  id!: string;

  @Column('varchar', { length: 30 })
  kind!: QueueKind;

  @Column('uuid', { name: 'subject_id' })
  subjectId!: string;

  @Index()
  @Column('varchar', { length: 20, default: 'waiting' })
  status!: QueueStatus;

  @Column('uuid', { name: 'assigned_to', nullable: true })
  assignedTo!: string | null;

  /** Yakunlagan yoki rad etgan xodim (reject da assigned_to bo'lmaydi) */
  @Column('uuid', { name: 'finished_by', nullable: true })
  finishedBy!: string | null;

  @Column('varchar', { length: 20, nullable: true })
  result!: QueueResult | null;

  @Column('varchar', { length: 1000, nullable: true })
  comment!: string | null;

  @Column('varchar', { name: 'reject_reason', length: 500, nullable: true })
  rejectReason!: string | null;

  /** Manba hodisaning event_id si — takroriy hodisadan himoya */
  @Index({ unique: true })
  @Column('uuid', { name: 'source_event_id', nullable: true })
  sourceEventId!: string | null;

  @Column('timestamptz', { name: 'created_at' })
  createdAt!: Date;

  @Column('timestamptz', { name: 'finished_at', nullable: true })
  finishedAt!: Date | null;
}
