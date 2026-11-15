import { Column, CreateDateColumn, Entity, Index, PrimaryColumn } from 'typeorm';

export type DocumentStatus = 'pending_upload' | 'uploaded';

@Entity('documents')
export class DriverDocument {
  @PrimaryColumn('uuid')
  id!: string;

  @Index()
  @Column('uuid', { name: 'driver_id' })
  driverId!: string;

  @Column('varchar', { length: 30 })
  kind!: string;

  @Column('varchar', { name: 'content_type', length: 30 })
  contentType!: string;

  /** Klient e'lon qilgan hajm (imzoga bog'liq, o'zgarmaydi; tozalangan fayl kichikroq bo'lishi mumkin) */
  @Column('int', { name: 'size_bytes' })
  sizeBytes!: number;

  @Column('varchar', { length: 20, default: 'pending_upload' })
  status!: DocumentStatus;

  /** Saqlash kaliti (lokal: nisbiy yo'l; keyin S3 kaliti) */
  @Column('varchar', { name: 'storage_key', length: 200, nullable: true })
  storageKey!: string | null;

  @CreateDateColumn({ name: 'created_at', type: 'timestamptz' })
  createdAt!: Date;

  @Column('timestamptz', { name: 'uploaded_at', nullable: true })
  uploadedAt!: Date | null;
}
