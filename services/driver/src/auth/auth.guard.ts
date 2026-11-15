import { CanActivate, ExecutionContext, Inject, Injectable, createParamDecorator } from '@nestjs/common';
import type { Request } from 'express';
import { Errors } from '../common/errors';
import { DriverStore } from '../drivers/store/driver.store';
import type { Driver } from '../drivers/entities/driver.entity';
import { JwtVerifier, JwtVerifyError } from './jwt.verifier';

export const JWT_VERIFIER = Symbol('JWT_VERIFIER');

export interface AuthedRequest extends Request {
  auth?: { userId: string; sessionId: string };
  driver?: Driver;
}

/**
 * Authorization: Bearer <JWT> ni tekshiradi va token egasining haydovchi yozuvini yuklaydi.
 * Egalik (F-10): hech qaysi endpoint driver_id qabul qilmaydi — faqat token `sub` (user_id) bo'yicha
 * topilgan yozuv bilan ishlanadi.
 */
@Injectable()
export class DriverAuthGuard implements CanActivate {
  constructor(
    @Inject(JWT_VERIFIER) private readonly verifier: JwtVerifier,
    private readonly store: DriverStore,
  ) {}

  async canActivate(ctx: ExecutionContext): Promise<boolean> {
    const req = ctx.switchToHttp().getRequest<AuthedRequest>();
    const header = req.headers.authorization;
    const m = typeof header === 'string' ? /^Bearer\s+(\S+)$/i.exec(header) : null;
    if (!m) throw Errors.tokenInvalid();

    try {
      const claims = this.verifier.verifyAccess(m[1]);
      if (claims.role !== undefined && claims.role !== 'driver') throw Errors.forbidden();
      req.auth = { userId: claims.sub, sessionId: claims.sid };
    } catch (e) {
      if (e instanceof JwtVerifyError) {
        if (e.reason === 'wrong_audience') throw Errors.forbidden();
        throw Errors.tokenInvalid();
      }
      throw e;
    }

    req.driver = await this.store.ensureDriver(req.auth.userId);
    return true;
  }
}

export const CurrentDriver = createParamDecorator((_d: unknown, ctx: ExecutionContext): Driver => {
  return ctx.switchToHttp().getRequest<AuthedRequest>().driver as Driver;
});
