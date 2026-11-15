import { Column, Entity, PrimaryColumn } from 'typeorm';

export const CHILD_SEAT_AGES = ['0-1', '1-3', '3-7', '7-12'] as const;
export type ChildSeatAge = (typeof CHILD_SEAT_AGES)[number];

/** "Imkoniyatlarim" (1:1). Yozuv bo'lmasa — hamma qiymat standart (false / []). */
@Entity('driver_capabilities')
export class DriverCapabilities {
  @PrimaryColumn('uuid', { name: 'driver_id' })
  driverId!: string;

  @Column('boolean', { name: 'child_seat', default: false })
  childSeat!: boolean;

  @Column('varchar', { name: 'child_seat_ages', length: 5, array: true, default: () => "'{}'" })
  childSeatAges!: ChildSeatAge[];

  @Column('boolean', { default: false })
  pets!: boolean;

  @Column('boolean', { name: 'big_luggage', default: false })
  bigLuggage!: boolean;

  @Column('timestamptz', { name: 'updated_at', default: () => 'now()' })
  updatedAt!: Date;
}
