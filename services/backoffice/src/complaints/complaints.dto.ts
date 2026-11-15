import { Transform } from 'class-transformer';
import { IsIn, IsNotEmpty, IsOptional, IsString, IsUUID, MaxLength, MinLength } from 'class-validator';
import { PageQuery } from '../common/pagination';
import {
  COMPLAINT_CATEGORIES, COMPLAINT_STATUSES, COMPLAINT_SUBJECT_TYPES, ComplaintCategory, ComplaintStatus, ComplaintSubjectType,
} from '../entities/complaint.entity';

const trim = () => Transform(({ value }) => (typeof value === 'string' ? value.trim() : value));

export class ComplaintListQuery extends PageQuery {
  @IsOptional() @IsIn(COMPLAINT_STATUSES)
  status?: ComplaintStatus;

  @IsOptional() @IsUUID()
  assigned_to?: string;

  @IsOptional() @IsIn(COMPLAINT_SUBJECT_TYPES)
  subject_type?: ComplaintSubjectType;
}

export class ComplaintCreateDto {
  @IsOptional() @IsUUID()
  order_id?: string;

  @IsIn(COMPLAINT_SUBJECT_TYPES)
  subject_type!: ComplaintSubjectType;

  @IsUUID()
  subject_id!: string;

  @IsIn(COMPLAINT_CATEGORIES)
  category!: ComplaintCategory;

  @trim() @IsString() @IsNotEmpty() @MaxLength(2000)
  description!: string;
}

export class ComplaintAssignDto {
  @IsUUID()
  staff_id!: string;
}

export const COMPLAINT_TARGET_STATUSES = ['in_progress', 'resolved', 'rejected'] as const;

export class ComplaintStatusDto {
  @IsIn(COMPLAINT_TARGET_STATUSES)
  status!: (typeof COMPLAINT_TARGET_STATUSES)[number];

  /** resolved / rejected uchun majburiy (3..1000) */
  @IsOptional() @trim() @IsString() @MinLength(3) @MaxLength(1000)
  resolution?: string;
}

export class ComplaintNoteDto {
  @trim() @IsString() @IsNotEmpty() @MaxLength(1000)
  text!: string;
}
