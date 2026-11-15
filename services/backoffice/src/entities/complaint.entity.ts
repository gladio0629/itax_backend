import { Column, Entity, Index, PrimaryColumn } from 'typeorm';

export const COMPLAINT_SUBJECT_TYPES = ['passenger', 'driver', 'order'] as const;
export const COMPLAINT_CATEGORIES = [
  'driver_behavior', 'passenger_behavior', 'payment', 'safety', 'lost_item', 'app_issue', 'other',
] as const;
export const COMPLAINT_STATUSES = ['open', 'in_progress', 'resolved', 'rejected'] as const;

export type ComplaintSubjectType = (typeof COMPLAINT_SUBJECT_TYPES)[number];
export type ComplaintCategory = (typeof COMPLAINT_CATEGORIES)[number];
export type ComplaintStatus = (typeof COMPLAINT_STATUSES)[number];

@Entity('complaints')
export class Complaint {
  @PrimaryColumn('uuid')
  id!: string;

  @Column('uuid', { name: 'order_id', nullable: true })
  orderId!: string | null;

  @Column('text', { name: 'subject_type' })
  subjectType!: ComplaintSubjectType;

  @Column('uuid', { name: 'subject_id' })
  subjectId!: string;

  @Column('text')
  category!: ComplaintCategory;

  @Column('text')
  description!: string;

  @Index()
  @Column('text', { default: 'open' })
  status!: ComplaintStatus;

  /** Mas'ul xodim (staff.id) */
  @Index()
  @Column('uuid', { name: 'assigned_to', nullable: true })
  assignedTo!: string | null;

  @Column('text', { nullable: true })
  resolution!: string | null;

  @Column('uuid', { name: 'created_by' })
  createdBy!: string;

  @Index()
  @Column('timestamptz', { name: 'created_at' })
  createdAt!: Date;

  @Column('timestamptz', { name: 'updated_at' })
  updatedAt!: Date;
}

@Entity('complaint_notes')
export class ComplaintNote {
  @PrimaryColumn('uuid')
  id!: string;

  @Index()
  @Column('uuid', { name: 'complaint_id' })
  complaintId!: string;

  @Column('uuid', { name: 'author_id' })
  authorId!: string;

  @Column('text')
  text!: string;

  @Column('timestamptz', { name: 'created_at' })
  createdAt!: Date;
}
