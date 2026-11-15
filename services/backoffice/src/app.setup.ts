import { INestApplication } from '@nestjs/common';
import { AppExceptionFilter } from './common/error.filter';
import { createValidationPipe } from './common/validation';

/** main.ts va testlar uchun umumiy sozlama. */
export function configureApp(app: INestApplication): void {
  app.useGlobalPipes(createValidationPipe());
  app.useGlobalFilters(new AppExceptionFilter());
}
