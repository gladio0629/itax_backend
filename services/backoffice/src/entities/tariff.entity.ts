import { Column, Entity, PrimaryColumn } from 'typeorm';

export type ApprovalStatus = 'pending_approval' | 'approved' | 'rejected';

export interface TariffValues {
  name: string;
  car_class: string;
  base_fare: number;
  per_km: number;
  per_min: number;
  min_fare: number;
  commission_percent: number;
}

export interface PendingChange {
  action: 'create' | 'update' | 'delete';
  requested_by: string;
  requested_at: string;
  /** update uchun taklif etilgan yangi qiymatlar */
  values?: TariffValues;
}

const num = { to: (v: number) => v, from: (v: string | number) => Number(v) };

@Entity('tariffs')
export class Tariff {
  @PrimaryColumn('uuid')
  id!: string;

  @Column('varchar', { length: 100 })
  name!: string;

  @Column('varchar', { name: 'car_class', length: 20 })
  carClass!: string;

  @Column('bigint', { name: 'base_fare', transformer: num })
  baseFare!: number;

  @Column('bigint', { name: 'per_km', transformer: num })
  perKm!: number;

  @Column('bigint', { name: 'per_min', transformer: num })
  perMin!: number;

  @Column('bigint', { name: 'min_fare', transformer: num })
  minFare!: number;

  @Column('numeric', { name: 'commission_percent', precision: 5, scale: 2, transformer: num })
  commissionPercent!: number;

  @Column('boolean', { default: false })
  active!: boolean;

  @Column('varchar', { name: 'approval_status', length: 20 })
  approvalStatus!: ApprovalStatus;

  @Column('jsonb', { name: 'pending_change', nullable: true })
  pendingChange!: PendingChange | null;

  /** Soft delete: o'chirish tasdiqlangach to'ldiriladi */
  @Column('timestamptz', { name: 'deleted_at', nullable: true })
  deletedAt!: Date | null;

  @Column('timestamptz', { name: 'created_at' })
  createdAt!: Date;

  @Column('timestamptz', { name: 'updated_at' })
  updatedAt!: Date;
}
