import { Transform } from 'class-transformer';
import { IsIn, IsOptional, IsRFC3339, IsString, IsUUID, MaxLength, ValidateIf } from 'class-validator';
import { PagedDateRangeQuery } from '../common/date-query';
import { CANCEL_REASONS, CancelReason, OrderPatch, PAYMENT_METHODS } from '../ports/ports';

export const ORDER_STATUSES = ['searching', 'accepted', 'arrived', 'in_progress', 'completed', 'cancelled', 'expired'];

const trim = () => Transform(({ value }) => (typeof value === 'string' ? value.trim() : value));
/** Maydon berilmasa — o'tkazib yuboriladi; `null` — rad etiladi (422), tozalash semantikasi yo'q */
const Present = () => ValidateIf((_o, v) => v !== undefined);

export class OrderListQuery extends PagedDateRangeQuery {
  @IsOptional() @IsIn(ORDER_STATUSES)
  status?: string;

  @IsOptional() @IsUUID()
  passenger_id?: string;

  /** Faqat mock rejimida qo'llanadi: trip-order /internal/orders da `q` yo'q — HTTP rejimida yuborilmaydi */
  @IsOptional() @trim() @IsString() @MaxLength(100)
  q?: string;
}

/** PATCH /admin/v1/orders/{id} — kamida bitta maydon; qo'shimcha qoidalar controllerda */
export class AdminOrderUpdateDto implements OrderPatch {
  @Present() @trim() @IsString() @MaxLength(200)
  comment?: string;

  @Present() @IsIn(PAYMENT_METHODS)
  payment_method?: OrderPatch['payment_method'];

  @Present() @IsRFC3339()
  scheduled_at?: string;

  @Present() @IsRFC3339()
  scheduled_from?: string;

  @Present() @IsRFC3339()
  scheduled_to?: string;
}

export class AdminOrderCancelDto {
  @IsIn(CANCEL_REASONS)
  reason!: CancelReason;

  @Present() @trim() @IsString() @MaxLength(500)
  comment?: string;
}
