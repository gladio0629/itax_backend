import { Transform } from 'class-transformer';
import { IsIn, IsOptional, IsString, MaxLength } from 'class-validator';
import { PagedDateRangeQuery } from '../common/date-query';

export const ORDER_STATUSES = ['searching', 'accepted', 'arrived', 'in_progress', 'completed', 'cancelled', 'expired'];

export class OrderListQuery extends PagedDateRangeQuery {
  @IsOptional() @IsIn(ORDER_STATUSES)
  status?: string;

  @IsOptional() @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value)) @IsString() @MaxLength(100)
  q?: string;
}
