import { PipeTransform } from '@nestjs/common';
import { Errors } from './errors';

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Yo'l parametri 1..4 butun son bo'lishi shart; aks holda 422 VALIDATION_ERROR. */
export class StepPipe implements PipeTransform<string, number> {
  transform(value: string): number {
    if (!/^[1-4]$/.test(value)) throw Errors.validation({ step: 'invalid' });
    return Number(value);
  }
}

export class UuidParamPipe implements PipeTransform<string, string> {
  constructor(private readonly field = 'id') {}
  transform(value: string): string {
    if (!UUID_RE.test(value)) throw Errors.validation({ [this.field]: 'isUuid' });
    return value.toLowerCase();
  }
}
