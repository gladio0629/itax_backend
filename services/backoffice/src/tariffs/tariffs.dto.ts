import { Transform } from 'class-transformer';
import { IsIn, IsInt, IsNotEmpty, IsNumber, IsString, Max, MaxLength, Min } from 'class-validator';

export class TariffInputDto {
  @Transform(({ value }) => (typeof value === 'string' ? value.trim() : value))
  @IsString() @IsNotEmpty() @MaxLength(100)
  name!: string;

  @IsIn(['economy', 'comfort', 'business', 'minivan'])
  car_class!: string;

  /** Barcha summalar tiyinda */
  @IsInt() @Min(0) @Max(1_000_000_000_000)
  base_fare!: number;

  @IsInt() @Min(0) @Max(1_000_000_000_000)
  per_km!: number;

  @IsInt() @Min(0) @Max(1_000_000_000_000)
  per_min!: number;

  @IsInt() @Min(0) @Max(1_000_000_000_000)
  min_fare!: number;

  @IsNumber({ maxDecimalPlaces: 2 }) @Min(0) @Max(100)
  commission_percent!: number;
}
