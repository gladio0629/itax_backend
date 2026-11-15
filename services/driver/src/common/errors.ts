import { HttpException } from '@nestjs/common';

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
  forbidden: (params: Record<string, unknown> = {}) => new AppError(403, 'FORBIDDEN', params),
  driverBlocked: (reason: string | null) => new AppError(403, 'DRIVER_BLOCKED', { reason: reason ?? 'blocked' }),
  driverNotFound: (driverId: string) => new AppError(404, 'DRIVER_NOT_FOUND', { driver_id: driverId }),
  internalOnly: () => new AppError(403, 'FORBIDDEN', { reason: 'internal_only' }),
  validation: (fields: Record<string, string>) => new AppError(422, 'VALIDATION_ERROR', { fields }),
  alreadySubmitted: () => new AppError(409, 'REGISTRATION_ALREADY_SUBMITTED'),
  documentAlreadyUploaded: () => new AppError(409, 'DOCUMENT_ALREADY_UPLOADED'),
  documentNotFound: (id: string) => new AppError(404, 'NOT_FOUND', { id }),
  fileTooLarge: (max: number) => new AppError(413, 'FILE_TOO_LARGE', { max_bytes: max }),
};
