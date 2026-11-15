import { CanActivate, ExecutionContext, Inject, Injectable } from '@nestjs/common';
import { timingSafeEqual } from 'crypto';
import type { Request } from 'express';
import { Errors } from '../common/errors';
import { APP_CONFIG, AppConfig } from '../config/configuration';

export const INTERNAL_TOKEN_HEADER = 'x-internal-token';

/**
 * /internal/* uchun X-Internal-Token tekshiruvi. Vaqt bo'yicha o'zgarmas taqqoslash, barcha tokenlar
 * tekshiriladi; token sozlanmagan bo'lsa hammasi rad etiladi (fail closed).
 */
@Injectable()
export class InternalTokenGuard implements CanActivate {
  private readonly tokens: Buffer[];

  constructor(@Inject(APP_CONFIG) cfg: AppConfig) {
    this.tokens = cfg.internalTokens.map((t) => Buffer.from(t));
  }

  canActivate(ctx: ExecutionContext): boolean {
    const raw = ctx.switchToHttp().getRequest<Request>().headers[INTERNAL_TOKEN_HEADER];
    const got = Buffer.from(typeof raw === 'string' ? raw : '');
    let ok = false;
    for (const t of this.tokens) {
      // uzunliklar farq qilsa timingSafeEqual xato beradi — tengsiz deb hisoblaymiz
      const match = got.length === t.length && timingSafeEqual(got, t);
      ok = ok || match;
    }
    if (!ok || got.length === 0) throw Errors.internalOnly();
    return true;
  }
}
