import { Transform } from 'class-transformer';
import { IsIn, IsOptional, IsString, MaxLength, MinLength } from 'class-validator';
import { PagedDateRangeQuery, } from '../common/date-query';
import { PageQuery } from '../common/pagination';

const trim = () => Transform(({ value }) => (typeof value === 'string' ? value.trim() : value));

export class QueueQuery extends PageQuery {
  /** waiting (default) | in_progress | completed_today (oxirgisi faqat yuqori rollar) */
  @IsOptional() @IsIn(['waiting', 'in_progress', 'completed_today'])
  section?: 'waiting' | 'in_progress' | 'completed_today';
}

export class HistoryQuery extends PagedDateRangeQuery {
  @IsOptional() @IsIn(['resolved', 'unresolved', 'rejected'])
  result?: 'resolved' | 'unresolved' | 'rejected';
}

export class QueueRejectDto {
  @trim() @IsString() @MinLength(3) @MaxLength(500)
  reason!: string;
}

export class QueueCompleteDto {
  @IsIn(['resolved', 'unresolved'])
  result!: 'resolved' | 'unresolved';

  @IsOptional() @IsString() @MaxLength(1000)
  comment?: string;
}
