import { Transform } from 'class-transformer';
import { IsEmail, IsIn, IsNotEmpty, IsOptional, IsString, Matches, MaxLength, ValidateIf } from 'class-validator';
import { PageQuery } from '../common/pagination';
import { STAFF_ROLES } from '../roles/roles.catalog';

const trim = () => Transform(({ value }) => (typeof value === 'string' ? value.trim() : value));
const PHONE = /^\+998[0-9]{9}$/;

export class StaffCreateDto {
  /** Operatordan boshqa hamma uchun majburiy (servisda tekshiriladi) */
  @ValidateIf((o: StaffCreateDto) => o.email !== undefined)
  @trim() @IsEmail() @MaxLength(254)
  email?: string;

  @Matches(PHONE)
  phone!: string;

  @trim() @IsString() @IsNotEmpty() @MaxLength(100)
  full_name!: string;

  @IsIn(STAFF_ROLES)
  role!: string;
}

export class StaffUpdateDto {
  @IsOptional() @Matches(PHONE)
  phone?: string;

  @IsOptional() @trim() @IsString() @IsNotEmpty() @MaxLength(100)
  full_name?: string;

  @IsOptional() @IsIn(STAFF_ROLES)
  role?: string;

  @IsOptional() @IsIn(['active', 'blocked'])
  state?: 'active' | 'blocked';
}

export class StaffListQuery extends PageQuery {
  @IsOptional() @IsIn(STAFF_ROLES)
  role?: string;

  @IsOptional() @IsIn(['active', 'blocked', 'archived'])
  state?: string;

  @IsOptional() @trim() @IsString() @MaxLength(100)
  q?: string;
}
