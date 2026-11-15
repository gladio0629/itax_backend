import { Column, CreateDateColumn, Entity, Index, PrimaryColumn, UpdateDateColumn } from 'typeorm';

export type DriverState = 'draft' | 'pending_review' | 'active' | 'rejected' | 'blocked';
export const DRIVER_STATES: DriverState[] = ['draft', 'pending_review', 'active', 'rejected', 'blocked'];

@Entity('drivers')
export class Driver {
  @PrimaryColumn('uuid')
  id!: string;

  /** identity dagi user ID (JWT `sub`) */
  @Index({ unique: true })
  @Column('uuid', { name: 'user_id' })
  userId!: string;

  /** user.created hodisasidan to'ldiriladi */
  @Column('varchar', { length: 13, nullable: true })
  phone!: string | null;

  @Column('varchar', { name: 'first_name', length: 50, nullable: true })
  firstName!: string | null;

  @Column('varchar', { name: 'last_name', length: 50, nullable: true })
  lastName!: string | null;

  @Column('date', { name: 'birth_date', nullable: true })
  birthDate!: string | null;

  @Column('varchar', { length: 50, nullable: true })
  city!: string | null;

  @Column('uuid', { name: 'avatar_document_id', nullable: true })
  avatarDocumentId!: string | null;

  @Index()
  @Column('varchar', { length: 20, default: 'draft' })
  state!: DriverState;

  @Column('varchar', { name: 'state_reason', length: 100, nullable: true })
  stateReason!: string | null;

  @Column('timestamptz', { name: 'state_updated_at', default: () => 'now()' })
  stateUpdatedAt!: Date;

  @Column('timestamptz', { name: 'submitted_at', nullable: true })
  submittedAt!: Date | null;

  @Column('numeric', { precision: 3, scale: 2, default: 0, transformer: { to: (v: number) => v, from: (v: string) => Number(v) } })
  rating!: number;

  @Column('int', { name: 'trips_count', default: 0 })
  tripsCount!: number;

  @CreateDateColumn({ name: 'created_at', type: 'timestamptz' })
  createdAt!: Date;

  @UpdateDateColumn({ name: 'updated_at', type: 'timestamptz' })
  updatedAt!: Date;
}
