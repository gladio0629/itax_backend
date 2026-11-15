import { Transform } from 'class-transformer';
import { IsIn, IsNotEmpty, IsOptional, IsString, Matches, MaxLength } from 'class-validator';
import { PageQuery } from '../common/pagination';

const trim = () => Transform(({ value }) => (typeof value === 'string' ? value.trim() : value));
const STATES = ['active', 'blocked', 'archived'] as const;

export class PersonCreateDto {
  @Matches(/^\+998[0-9]{9}$/)
  phone!: string;

  @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  first_name!: string;

  @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  last_name!: string;
}

export class PersonUpdateDto {
  @IsOptional() @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  first_name?: string;

  @IsOptional() @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  last_name?: string;

  @IsOptional() @IsIn(STATES)
  state?: 'active' | 'blocked' | 'archived';

  @IsOptional() @trim() @IsString() @IsNotEmpty() @MaxLength(200)
  state_reason?: string;
}

/** PUT /{id}/block */
export class BlockDto {
  @trim() @IsString() @IsNotEmpty() @MaxLength(200)
  state_reason!: string;
}

export class PersonListQuery extends PageQuery {
  @IsOptional() @IsIn(STATES)
  state?: 'active' | 'blocked' | 'archived';

  @IsOptional() @trim() @IsString() @MaxLength(100)
  q?: string;
}
