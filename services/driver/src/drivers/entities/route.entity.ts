import { Column, Entity, Index, PrimaryColumn } from 'typeorm';

/**
 * "Yo'nalishlarim": haydovchi ishlaydigan shaharlararo yo'nalish (A → B, ixtiyoriy ravishda B → A ham).
 * a_city_id / b_city_id — Trip & Order shahar katalogidagi ID lar (GET /api/v1/cities); Driver servisi
 * katalogga kira olmaydi, shuning uchun faqat UUID formati tekshiriladi, mavjudligi emas.
 */
@Entity('driver_routes')
export class DriverRoute {
  @PrimaryColumn('uuid')
  id!: string;

  @Index()
  @Column('uuid', { name: 'driver_id' })
  driverId!: string;

  @Column('uuid', { name: 'a_city_id' })
  aCityId!: string;

  @Column('uuid', { name: 'b_city_id' })
  bCityId!: string;

  @Column('boolean', { name: 'both_ways', default: false })
  bothWays!: boolean;

  /** PUT dagi massiv tartibi (bir tranzaksiyadagi created_at lar bir xil bo'ladi — tartib shu ustun bo'yicha) */
  @Column('smallint')
  position!: number;

  @Column('timestamptz', { name: 'created_at', default: () => 'now()' })
  createdAt!: Date;
}
