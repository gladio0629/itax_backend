import { INestApplication } from '@nestjs/common';
import { raw } from 'express';
import { AppExceptionFilter } from './common/error.filter';
import { createValidationPipe } from './common/validation';
import { MAX_UPLOAD_BYTES } from './drivers/dto/request.dto';

/** main.ts va testlar uchun umumiy sozlama. */
export function configureApp(app: INestApplication): void {
  // Hujjat yuklash: xom JPEG/PNG bayt (JSON limiti 100 kb — bu yerga tegishli emas)
  app.use('/api/v1/drivers/documents', raw({ type: ['image/jpeg', 'image/png'], limit: MAX_UPLOAD_BYTES }));
  app.useGlobalPipes(createValidationPipe());
  app.useGlobalFilters(new AppExceptionFilter());
}
