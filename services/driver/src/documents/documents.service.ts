import { Inject, Injectable, Logger } from '@nestjs/common';
import { v4 as uuidv4 } from 'uuid';
import { Errors } from '../common/errors';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { assertNotBlocked } from '../drivers/drivers.service';
import { UploadUrlRequestDto } from '../drivers/dto/request.dto';
import { UploadUrlResponseDto } from '../drivers/dto/response.dto';
import type { Driver } from '../drivers/entities/driver.entity';
import { DriverStore } from '../drivers/store/driver.store';
import { detectImageType, InvalidImageError, stripMetadata } from './image-sanitizer';
import { FileStorage } from './storage.service';
import { UploadSigner } from './upload-signer';

export const UPLOAD_SIGNER = Symbol('UPLOAD_SIGNER');
const EXT: Record<string, string> = { 'image/jpeg': 'jpg', 'image/png': 'png' };

@Injectable()
export class DocumentsService {
  private readonly log = new Logger(DocumentsService.name);

  constructor(
    private readonly store: DriverStore,
    private readonly storage: FileStorage,
    @Inject(UPLOAD_SIGNER) private readonly signer: UploadSigner,
    @Inject(APP_CONFIG) private readonly cfg: AppConfig,
  ) {}

  async createUploadUrl(driver: Driver, dto: UploadUrlRequestDto): Promise<UploadUrlResponseDto> {
    assertNotBlocked(driver);
    const doc = await this.store.createDocument({
      id: uuidv4(),
      driverId: driver.id,
      kind: dto.kind,
      contentType: dto.content_type,
      sizeBytes: dto.size_bytes,
    });
    const { expires, signature } = this.signer.sign(
      { documentId: doc.id, driverId: driver.id, contentType: doc.contentType, sizeBytes: doc.sizeBytes },
      this.cfg.uploads.urlTtlSeconds,
    );
    return {
      document_id: doc.id,
      upload_url: `${this.cfg.uploads.publicBaseUrl}/api/v1/drivers/documents/${doc.id}/content?expires=${expires}&signature=${signature}`,
      method: 'PUT',
      headers: { 'Content-Type': doc.contentType },
      expires_at: new Date(expires * 1000).toISOString(),
    };
  }

  /**
   * Imzolangan URL ga PUT. Imzo hujjat egasi (driver_id), tur, hajm va muddatni bog'laydi (F-10):
   * boshqa haydovchining hujjatiga yozib bo'lmaydi.
   */
  async upload(documentId: string, expires: string, signature: string, contentType: string | undefined, body: unknown): Promise<void> {
    const doc = await this.store.findDocument(documentId);
    const exp = Number(expires);
    if (!doc || !Number.isInteger(exp) || !/^[0-9a-f]{64}$/i.test(signature ?? '')) {
      throw Errors.forbidden(); // mavjudlik oshkor bo'lmasin
    }
    const check = this.signer.check(
      { documentId: doc.id, driverId: doc.driverId, contentType: doc.contentType, sizeBytes: doc.sizeBytes, expires: exp },
      signature,
    );
    if (check === 'invalid') throw Errors.forbidden();
    if (check === 'expired') throw Errors.forbidden({ reason: 'upload_url_expired' });
    if (doc.status === 'uploaded') throw Errors.documentAlreadyUploaded();

    const owner = await this.store.findById(doc.driverId);
    if (owner) assertNotBlocked(owner);

    if ((contentType ?? '').split(';')[0].trim().toLowerCase() !== doc.contentType) {
      throw Errors.validation({ content_type: 'mismatch' });
    }
    if (!Buffer.isBuffer(body) || body.length === 0) throw Errors.validation({ file: 'empty' });
    if (body.length !== doc.sizeBytes) throw Errors.validation({ size_bytes: 'mismatch' });

    // Sarlavhaga ishonmaymiz: haqiqiy turi fayl boshidan aniqlanadi
    const detected = detectImageType(body);
    if (detected !== doc.contentType) throw Errors.validation({ file: 'invalid_image' });

    let clean: Buffer;
    try {
      clean = stripMetadata(body, detected); // F-17: EXIF/GPS va boshqa metadata o'chiriladi
    } catch (e) {
      if (e instanceof InvalidImageError) throw Errors.validation({ file: 'invalid_image' });
      throw e;
    }

    // Har urinish uchun noyob kalit: parallel PUT lar bir-birining faylini ustidan yozmaydi
    const key = `${doc.driverId}/${doc.id}-${uuidv4()}.${EXT[detected]}`;
    await this.storage.put(key, clean);
    const won = await this.store.markDocumentUploaded(doc.id, key);
    if (!won) {
      await this.storage.delete(key).catch((err: Error) => this.log.warn(`orphan fayl o'chmadi: ${err.message}`));
      throw Errors.documentAlreadyUploaded();
    }
  }
}
