import { HttpException } from '@nestjs/common';

/** Postgres unique cheklovi buzildi (23505) — band email/telefon yoki parallel yaratilgan bir xil yozuv. */
export const isUniqueViolation = (e: unknown): boolean => {
  const err = e as { code?: string; message?: string };
  return err?.code === '23505' || /unique|duplicate/i.test(err?.message ?? '');
};

/** Xato formati: {"error":{"key":"...","params":{}}} (shared/errors/errors.json). */
export class AppError extends HttpException {
  constructor(
    status: number,
    public readonly key: string,
    public readonly params: Record<string, unknown> = {},
  ) {
    super({ error: { key, params } }, status);
  }
}

export const Errors = {
  tokenInvalid: () => new AppError(401, 'AUTH_TOKEN_EXPIRED'),
  forbidden: (reason?: string) => new AppError(403, 'FORBIDDEN', reason ? { reason } : {}),
  validation: (fields: Record<string, string>) => new AppError(422, 'VALIDATION_ERROR', { fields }),
  notFound: (id: string) => new AppError(404, 'NOT_FOUND', { id }),
  staffNotFound: (id: string) => new AppError(404, 'STAFF_NOT_FOUND', { staff_id: id }),
  driverNotFound: (id: string) => new AppError(404, 'DRIVER_NOT_FOUND', { driver_id: id }),
  passengerNotFound: (id: string) => new AppError(404, 'PASSENGER_NOT_FOUND', { passenger_id: id }),
  orderNotFound: (id: string) => new AppError(404, 'ORDER_NOT_FOUND', { order_id: id }),
  tariffNotFound: (id: string) => new AppError(404, 'TARIFF_NOT_FOUND', { tariff_id: id }),
  staffEmailTaken: () => new AppError(409, 'STAFF_EMAIL_TAKEN'),
  staffPhoneTaken: () => new AppError(409, 'STAFF_PHONE_TAKEN'),
  tariffChangePending: () => new AppError(409, 'TARIFF_CHANGE_PENDING'),
  /** Identity (yoki boshqa ichki servis) javob bermadi / kutilmagan javob berdi */
  identityUnavailable: (reason: string) => new AppError(502, 'IDENTITY_UNAVAILABLE', { reason }),
  /** Trip & Order ichki API javob bermadi / kutilmagan javob berdi */
  tripOrderUnavailable: (reason: string) => new AppError(502, 'TRIP_ORDER_UNAVAILABLE', { reason }),
  idempotencyConflict: () => new AppError(409, 'IDEMPOTENCY_KEY_REUSED'),
};
