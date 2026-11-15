import { Column, Entity, PrimaryColumn } from 'typeorm';

@Entity('cars')
export class Car {
  @PrimaryColumn('uuid', { name: 'driver_id' })
  driverId!: string;

  @Column('varchar', { length: 50 })
  brand!: string;

  @Column('varchar', { length: 50 })
  model!: string;

  @Column('int')
  year!: number;

  @Column('varchar', { length: 30 })
  color!: string;

  @Column('varchar', { name: 'plate_number', length: 15 })
  plateNumber!: string;

  @Column('varchar', { name: 'car_class', length: 20 })
  carClass!: string;

  @Column('smallint')
  seats!: number;

  @Column('timestamptz', { name: 'updated_at', default: () => 'now()' })
  updatedAt!: Date;
}
