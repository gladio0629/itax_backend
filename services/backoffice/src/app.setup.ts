import { INestApplication } from '@nestjs/common';
import type { Express } from 'express';
import { AppExceptionFilter } from './common/error.filter';
import { securityHeaders } from './common/security-headers';
import { createValidationPipe } from './common/validation';

/** main.ts va testlar uchun umumiy sozlama. */
export function configureApp(app: INestApplication): void {
  (app.getHttpAdapter().getInstance() as Express).disable('x-powered-by');
  // Marshrutlardan oldin: guard (401/403), filter (404/422) va 200 javoblari ham header oladi
  app.use(securityHeaders);
  app.useGlobalPipes(createValidationPipe());
  app.useGlobalFilters(new AppExceptionFilter());
}
