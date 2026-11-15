import { Transform } from 'class-transformer';
import { IsIn, IsInt, IsNotEmpty, IsOptional, IsString, IsUUID, MaxLength, MinLength, NotEquals, Max, Min } from 'class-validator';
import { PageQuery } from '../common/pagination';

const trim = () => Transform(({ value }) => (typeof value === 'string' ? value.trim() : value));

export class LedgerQuery extends PageQuery {
  @IsOptional() @IsUUID()
  wallet_id?: string;

  @IsOptional() @IsUUID()
  driver_id?: string;
}

export const APPROVAL_STATUSES = ['pending_approval', 'approved', 'rejected'] as const;

/** GET /wallet/correction — tuzatishlar ro'yxati (masalan tasdiq kutayotganlar) */
export class CorrectionListQuery extends PageQuery {
  @IsOptional() @IsIn(APPROVAL_STATUSES)
  approval_status?: (typeof APPROVAL_STATUSES)[number];
}

export class CorrectionCreateDto {
  @IsUUID()
  wallet_id!: string;

  /** Ishorali summa, tiyinda; 0 emas */
  @IsInt() @NotEquals(0) @Min(-1_000_000_000_000) @Max(1_000_000_000_000)
  amount!: number;

  @trim() @IsString() @IsNotEmpty() @MinLength(5) @MaxLength(500)
  reason!: string;
}

/** Wallet va tariff uchun umumiy */
export class ApprovalRejectDto {
  @trim() @IsString() @MinLength(3) @MaxLength(500)
  reason!: string;
}
