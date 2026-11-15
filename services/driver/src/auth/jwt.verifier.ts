import { createPublicKey, KeyObject, verify } from 'crypto';

export interface AccessClaims {
  sub: string;
  sid: string;
  jti: string;
  aud: string;
  role?: string;
  exp: number;
}

export type VerifyFailure = 'invalid' | 'expired' | 'wrong_audience';

export class JwtVerifyError extends Error {
  constructor(public readonly reason: VerifyFailure) {
    super(`jwt: ${reason}`);
  }
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * Identity servisi chiqargan Ed25519 (EdDSA) access token'ni tekshiradi.
 * Identity Parse() bilan bir xil qoidalar: alg=EdDSA, iss, exp majburiy, typ=access,
 * bitta aud, sub/sid/jti bo'sh emas.
 */
export class JwtVerifier {
  private readonly key: KeyObject;

  constructor(
    publicKeyPem: string,
    private readonly issuer: string,
    private readonly audience: string,
    private readonly now: () => number = () => Date.now(),
  ) {
    this.key = createPublicKey(publicKeyPem);
    if (this.key.asymmetricKeyType !== 'ed25519') {
      throw new Error('JWT public kalit Ed25519 bo\'lishi kerak');
    }
  }

  verifyAccess(raw: string): AccessClaims {
    const parts = raw.split('.');
    if (parts.length !== 3 || parts.some((p) => p.length === 0)) throw new JwtVerifyError('invalid');
    const [h, p, s] = parts;

    let header: { alg?: string };
    let claims: Record<string, unknown>;
    let sig: Buffer;
    try {
      header = JSON.parse(Buffer.from(h, 'base64url').toString('utf8'));
      claims = JSON.parse(Buffer.from(p, 'base64url').toString('utf8'));
      sig = Buffer.from(s, 'base64url');
    } catch {
      throw new JwtVerifyError('invalid');
    }
    if (header?.alg !== 'EdDSA' || typeof claims !== 'object' || claims === null) {
      throw new JwtVerifyError('invalid');
    }
    let ok = false;
    try {
      ok = verify(null, Buffer.from(`${h}.${p}`), this.key, sig);
    } catch {
      ok = false;
    }
    if (!ok) throw new JwtVerifyError('invalid');

    const nowSec = Math.floor(this.now() / 1000);
    if (typeof claims.exp !== 'number') throw new JwtVerifyError('invalid');
    if (claims.iss !== this.issuer) throw new JwtVerifyError('invalid');
    if (typeof claims.nbf === 'number' && claims.nbf > nowSec) throw new JwtVerifyError('invalid');
    if (claims.exp <= nowSec) throw new JwtVerifyError('expired');

    const aud = claims.aud;
    const audValue = typeof aud === 'string' ? aud : Array.isArray(aud) && aud.length === 1 ? aud[0] : null;
    if (
      claims.typ !== 'access' ||
      typeof claims.sub !== 'string' || !UUID_RE.test(claims.sub) ||
      typeof claims.sid !== 'string' || !claims.sid ||
      typeof claims.jti !== 'string' || !claims.jti ||
      typeof audValue !== 'string'
    ) {
      throw new JwtVerifyError('invalid');
    }
    if (audValue !== this.audience) throw new JwtVerifyError('wrong_audience');

    return {
      sub: claims.sub,
      sid: claims.sid,
      jti: claims.jti,
      aud: audValue,
      role: typeof claims.role === 'string' ? claims.role : undefined,
      exp: claims.exp,
    };
  }
}
