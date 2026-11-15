import { Column, Entity, Index, PrimaryColumn } from 'typeorm';
import type { StaffRole } from '../roles/roles.catalog';

export type StaffState = 'active' | 'blocked' | 'archived';

@Entity('staff')
export class Staff {
  /** identity dagi staff_users.id bilan bir xil (token `sub`) */
  @PrimaryColumn('uuid')
  id!: string;

  /** Kichik harfda saqlanadi (normalizatsiya). Operator uchun bo'sh bo'lishi mumkin. */
  @Index({ unique: true })
  @Column('varchar', { length: 254, nullable: true })
  email!: string | null;

  @Index({ unique: true })
  @Column('varchar', { length: 13, nullable: true })
  phone!: string | null;

  @Column('varchar', { name: 'full_name', length: 100, default: '' })
  fullName!: string;

  @Column('varchar', { length: 20 })
  role!: StaffRole;

  @Column('varchar', { length: 20, default: 'active' })
  state!: StaffState;

  @Column('uuid', { name: 'created_by', nullable: true })
  createdBy!: string | null;

  @Column('timestamptz', { name: 'created_at' })
  createdAt!: Date;

  @Column('timestamptz', { name: 'updated_at' })
  updatedAt!: Date;
}
