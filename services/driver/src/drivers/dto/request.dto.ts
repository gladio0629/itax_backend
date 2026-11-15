import { Transform, Type } from 'class-transformer';
import {
  ArrayMaxSize,
  ArrayMinSize,
  ArrayUnique,
  Equals,
  IsArray,
  IsBoolean,
  IsIn,
  IsInt,
  IsNotEmpty,
  IsOptional,
  IsString,
  IsUUID,
  Matches,
  Max,
  MaxLength,
  Min,
  ValidateNested,
} from 'class-validator';
import { IsUuidFormat } from '../../common/validation';
import { CHILD_SEAT_AGES, ChildSeatAge } from '../entities/capabilities.entity';

const trim = () => Transform(({ value }) => (typeof value === 'string' ? value.trim() : value));

export const DOCUMENT_KINDS = [
  'license_front', 'license_back', 'tech_passport_front', 'tech_passport_back',
  'passport', 'selfie', 'car_photo', 'avatar',
] as const;
export type DocumentKind = (typeof DOCUMENT_KINDS)[number];

export const CAR_CLASSES = ['economy', 'comfort', 'business', 'minivan'] as const;

/** Faqat JPEG va PNG (OpenAPI dagi application/pdf bu servisda qabul qilinmaydi). */
export const ALLOWED_CONTENT_TYPES = ['image/jpeg', 'image/png'] as const;
export const MAX_UPLOAD_BYTES = 10 * 1024 * 1024;

// ------------------------------------------------------------ registration

export class Step1Dto {
  @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  first_name!: string;

  @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  last_name!: string;

  @Matches(/^\d{4}-\d{2}-\d{2}$/)
  birth_date!: string;

  @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  city!: string;
}

export class CarDto {
  @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  brand!: string;

  @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  model!: string;

  @IsInt() @Min(1990) @Max(2100)
  year!: number;

  @trim() @IsString() @IsNotEmpty() @MaxLength(30)
  color!: string;

  @Transform(({ value }) => (typeof value === 'string' ? value.replace(/\s+/g, '').toUpperCase() : value))
  @IsString() @IsNotEmpty() @MaxLength(15) @Matches(/^[A-Z0-9]+$/)
  plate_number!: string;

  @IsIn(CAR_CLASSES)
  car_class!: string;

  @IsInt() @Min(1) @Max(8)
  seats!: number;
}

export class Step2Dto {
  @ValidateNested() @Type(() => CarDto)
  car!: CarDto;
}

export class DocumentRefDto {
  @IsIn(DOCUMENT_KINDS.filter((k) => k !== 'avatar'))
  kind!: DocumentKind;

  @IsUUID()
  document_id!: string;
}

export class Step3Dto {
  @IsArray() @ArrayMinSize(1) @ArrayMaxSize(10)
  @ValidateNested({ each: true }) @Type(() => DocumentRefDto)
  documents!: DocumentRefDto[];
}

export class Step4Dto {
  @Equals(true)
  accept_terms!: true;
}

// ----------------------------------------------------------------- profile

/** F-09: faqat shu maydonlar o'zgaradi (whitelist + forbidNonWhitelisted). */
export class UpdateProfileDto {
  @IsOptional() @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  first_name?: string;

  @IsOptional() @trim() @IsString() @IsNotEmpty() @MaxLength(50)
  last_name?: string;

  @IsOptional() @IsUUID()
  avatar_document_id?: string;
}

// --------------------------------------------------------------- documents

export class UploadUrlRequestDto {
  @IsIn(DOCUMENT_KINDS)
  kind!: DocumentKind;

  @IsIn(ALLOWED_CONTENT_TYPES)
  content_type!: (typeof ALLOWED_CONTENT_TYPES)[number];

  @IsInt() @Min(1) @Max(MAX_UPLOAD_BYTES)
  size_bytes!: number;
}

// ------------------------------------------------------- yo'nalishlarim

export const MAX_ROUTES = 10;

/**
 * Shahar ID lari Trip & Order katalogidan (GET /api/v1/cities). Driver servisi katalogga ulanmaydi —
 * faqat UUID formati tekshiriladi, shahar mavjudligi tekshirilmaydi.
 */
export class RouteInputDto {
  @IsUuidFormat()
  a_city_id!: string;

  @IsUuidFormat()
  b_city_id!: string;

  @IsBoolean()
  both_ways!: boolean;
}

/** PUT /me/routes: butun to'plam almashtiriladi (0..10 ta). Takror/bir xil shahar — servisda tekshiriladi. */
export class ReplaceRoutesDto {
  // Dekoratorlar teskari tartibda qo'llanadi: birinchi xato kaliti eng pastdagisidan (massiv emas → isArray)
  @ValidateNested({ each: true }) @Type(() => RouteInputDto)
  @ArrayMaxSize(MAX_ROUTES) @IsArray()
  routes!: RouteInputDto[];
}

// ------------------------------------------------------ imkoniyatlarim

/** PUT /me/capabilities: to'rt maydon ham majburiy. child_seat=false da yoshlar bo'sh — servisda tekshiriladi. */
export class CapabilitiesDto {
  @IsBoolean()
  child_seat!: boolean;

  @IsIn(CHILD_SEAT_AGES, { each: true }) @ArrayUnique() @ArrayMaxSize(CHILD_SEAT_AGES.length) @IsArray()
  child_seat_ages!: ChildSeatAge[];

  @IsBoolean()
  pets!: boolean;

  @IsBoolean()
  big_luggage!: boolean;
}

// ------------------------------------------------------ ichki statistika

/** GET /internal/driver/stats so'rov parametrlari (YYYY-MM-DD, UTC). Format va sananing haqiqiyligi servisda (`invalid_date`). */
export class DriverStatsQueryDto {
  @IsOptional() @IsString()
  from_date?: string;

  @IsOptional() @IsString()
  to_date?: string;
}
