import { ValidationError, ValidationPipe } from '@nestjs/common';
import { plainToInstance } from 'class-transformer';
import { validate, ValidateBy, ValidationOptions } from 'class-validator';
import { Errors } from './errors';
import { UUID_RE } from './pipes';

/** class-validator xatolarini {maydon: xato_kaliti} xaritasiga aylantiradi (ichma-ich maydonlar "car.brand"). */
export function flattenErrors(errors: ValidationError[], prefix = ''): Record<string, string> {
  const out: Record<string, string> = {};
  for (const e of errors) {
    const path = prefix ? `${prefix}.${e.property}` : e.property;
    if (e.constraints) {
      out[path] = Object.keys(e.constraints)[0];
    }
    if (e.children?.length) {
      Object.assign(out, flattenErrors(e.children, path));
    }
  }
  return out;
}

export const VALIDATION_OPTIONS = {
  whitelist: true,
  forbidNonWhitelisted: true,
  transform: true,
} as const;

export function createValidationPipe(): ValidationPipe {
  return new ValidationPipe({
    ...VALIDATION_OPTIONS,
    exceptionFactory: (errors) => Errors.validation(flattenErrors(errors)),
  });
}

/** Qadam raqamiga qarab tanlanadigan DTO uchun qo'lda validatsiya. */
export async function validateDto<T extends object>(cls: new () => T, body: unknown): Promise<T> {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) {
    throw Errors.validation({ body: 'isObject' });
  }
  const inst = plainToInstance(cls, body);
  const errors = await validate(inst, VALIDATION_OPTIONS);
  if (errors.length > 0) throw Errors.validation(flattenErrors(errors));
  return inst;
}

/**
 * UUID formati (8-4-4-4-12 hex). Xato kaliti — `invalid_format`.
 * class-validator dagi @IsUUID versiya/variantni ham talab qiladi va 00000000-...-0001 kabi ID larni rad etadi.
 */
export function IsUuidFormat(opts?: ValidationOptions): PropertyDecorator {
  return ValidateBy({
    name: 'invalid_format',
    validator: { validate: (v: unknown) => typeof v === 'string' && UUID_RE.test(v) },
  }, opts);
}
