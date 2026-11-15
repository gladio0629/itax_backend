import { PipeTransform } from '@nestjs/common';
import { Errors } from './errors';

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export const isUuid = (v: unknown): v is string => typeof v === 'string' && UUID_RE.test(v);

/** Yo'l parametri UUID bo'lishi shart; aks holda 422 VALIDATION_ERROR. */
export class UuidParamPipe implements PipeTransform<string, string> {
  constructor(private readonly field = 'id') {}
  transform(value: string): string {
    if (!isUuid(value)) throw Errors.validation({ [this.field]: 'isUuid' });
    return value.toLowerCase();
  }
}
