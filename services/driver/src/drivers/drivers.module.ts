import { Module } from '@nestjs/common';
import { ApprovalService } from '../approval/approval.service';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { DocumentsController } from '../documents/documents.controller';
import { DocumentsService, UPLOAD_SIGNER } from '../documents/documents.service';
import { FileStorage, LocalFileStorage } from '../documents/storage.service';
import { UploadSigner } from '../documents/upload-signer';
import { InternalDriversController } from './internal-drivers.controller';
import { DriversController } from './drivers.controller';
import { DriversService } from './drivers.service';
import { RegistrationService } from './registration.service';

@Module({
  controllers: [DriversController, DocumentsController, InternalDriversController],
  providers: [
    DriversService,
    RegistrationService,
    DocumentsService,
    ApprovalService,
    { provide: FileStorage, useClass: LocalFileStorage },
    { provide: UPLOAD_SIGNER, inject: [APP_CONFIG], useFactory: (c: AppConfig) => new UploadSigner(c.uploads.signingSecret) },
  ],
})
export class DriversModule {}
