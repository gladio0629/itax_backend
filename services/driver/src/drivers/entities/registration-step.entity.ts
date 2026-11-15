import { Column, Entity, PrimaryColumn } from 'typeorm';

/** Ro'yxatning har bir qadami alohida saqlanadi: (driver_id, step) — yagona. */
@Entity('registration_steps')
export class RegistrationStep {
  @PrimaryColumn('uuid', { name: 'driver_id' })
  driverId!: string;

  @PrimaryColumn('smallint')
  step!: number;

  @Column('jsonb')
  data!: Record<string, unknown>;

  @Column('timestamptz', { name: 'completed_at', default: () => 'now()' })
  completedAt!: Date;
}
