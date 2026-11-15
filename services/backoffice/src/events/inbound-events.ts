import { Injectable } from '@nestjs/common';
import { isUuid } from '../common/pipes';
import { OperatorService } from '../operator/operator.service';

/** Kiruvchi hodisalar. Idempotent: bir hodisa ikki marta kelsa navbatga ikki marta tushmaydi. */
@Injectable()
export class InboundEvents {
  constructor(private readonly operator: OperatorService) {}

  /** driver: driver.submitted -> operator navbatiga "driver_application". */
  async onDriverSubmitted(env: unknown): Promise<void> {
    const e = env as { event_id?: unknown; payload?: { driver_id?: unknown } };
    if (!isUuid(e?.event_id) || !isUuid(e?.payload?.driver_id)) throw new SyntaxError('driver.submitted: noto\'g\'ri format');
    await this.operator.enqueueDriverApplication(e.event_id, e.payload.driver_id);
  }
}
