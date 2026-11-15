import { createHmac, timingSafeEqual } from 'crypto';

export interface UploadClaims {
  documentId: string;
  driverId: string;
  contentType: string;
  sizeBytes: number;
  expires: number; // unix soniya
}

/** Imzolangan yuklash URL i (S3 presigned URL ga o'xshash): HMAC-SHA256, qisqa muddat. */
export class UploadSigner {
  constructor(private readonly secret: string, private readonly now: () => number = () => Date.now()) {}

  sign(c: Omit<UploadClaims, 'expires'>, ttlSeconds: number): { expires: number; signature: string } {
    const expires = Math.floor(this.now() / 1000) + ttlSeconds;
    return { expires, signature: this.mac({ ...c, expires }) };
  }

  /** 'ok' | 'expired' | 'invalid' */
  check(c: UploadClaims, signature: string): 'ok' | 'expired' | 'invalid' {
    const expected = Buffer.from(this.mac(c), 'hex');
    let given: Buffer;
    try {
      given = Buffer.from(signature, 'hex');
    } catch {
      return 'invalid';
    }
    if (given.length !== expected.length || !timingSafeEqual(given, expected)) return 'invalid';
    return c.expires <= Math.floor(this.now() / 1000) ? 'expired' : 'ok';
  }

  private mac(c: UploadClaims): string {
    return createHmac('sha256', this.secret)
      .update([c.documentId, c.driverId, c.contentType, c.sizeBytes, c.expires].join('\n'))
      .digest('hex');
  }
}
