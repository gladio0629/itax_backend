import { IsOptional, Matches } from 'class-validator';
import { PageQuery } from './pagination';

const DATE = /^\d{4}-\d{2}-\d{2}$/;

export class DateRangeQuery {
  @IsOptional() @Matches(DATE)
  from_date?: string;

  @IsOptional() @Matches(DATE)
  to_date?: string;
}

export class PagedDateRangeQuery extends PageQuery {
  @IsOptional() @Matches(DATE)
  from_date?: string;

  @IsOptional() @Matches(DATE)
  to_date?: string;
}
