import { Column, Entity, Index, PrimaryColumn } from 'typeorm';
import type { ApprovalStatus } from './tariff.entity';

@Entity('corrections')
@Index(['createdBy', 'idempotencyKey'], { unique: true })
export class Correction {
  @PrimaryColumn('uuid')
  id!: string;

  @Column('uuid', { name: 'wallet_id' })
  walletId!: string;

  @Column('bigint', { transformer: { to: (v: number) => v, from: (v: string | number) => Number(v) } })
  amount!: number;

  @Column('varchar', { length: 500 })
  reason!: string;

  @Index()
  @Column('varchar', { name: 'approval_status', length: 20 })
  approvalStatus!: ApprovalStatus;

  @Column('uuid', { name: 'created_by' })
  createdBy!: string;

  @Column('uuid', { name: 'approved_by', nullable: true })
  approvedBy!: string | null;

  @Column('varchar', { name: 'reject_reason', length: 500, nullable: true })
  rejectReason!: string | null;

  @Column('uuid', { name: 'idempotency_key', nullable: true })
  idempotencyKey!: string | null;

  /** Idempotency-Key bilan kelgan so'rov tanasining xeshi: boshqa tana bilan qayta ishlatish — xato */
  @Column('varchar', { name: 'request_hash', length: 64, nullable: true })
  requestHash!: string | null;

  @Column('timestamptz', { name: 'created_at' })
  createdAt!: Date;

  @Column('timestamptz', { name: 'decided_at', nullable: true })
  decidedAt!: Date | null;
}
