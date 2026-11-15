import { CanActivate, ExecutionContext, Inject, Injectable, SetMetadata, createParamDecorator } from '@nestjs/common';
import { Reflector } from '@nestjs/core';
import { DataSource } from 'typeorm';
import type { Request } from 'express';
import { Actor, AuditService } from '../audit/audit.service';
import { Errors } from '../common/errors';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { Staff } from '../entities/staff.entity';
import { ROLES, RolesCatalog, StaffRole } from '../roles/roles.catalog';
import { JwtVerifier, JwtVerifyError } from './jwt.verifier';

export const JWT_VERIFIER = Symbol('JWT_VERIFIER');
const PERM_KEY = 'perm';
const PUBLIC_KEY = 'public';
const AUTHED_KEY = 'authed';

/** Endpoint uchun kerakli ruxsat (roles.json dagi `<bo'lim>:read|write`). Deklaratsiyasiz endpoint — rad etiladi. */
export const Perm = (permission: string) => SetMetadata(PERM_KEY, permission);
/** Faqat tokenli xodim bo'lishi yetarli (masalan /me). */
export const Authenticated = () => SetMetadata(AUTHED_KEY, true);
export const Public = () => SetMetadata(PUBLIC_KEY, true);

export interface AdminRequest extends Request {
  staff?: Staff;
  actor?: Actor;
}

/**
 * Global guard (default-deny):
 *  1. Authorization: Bearer — Ed25519 admin JWT (aud=admin, typ=access) tekshiriladi;
 *  2. xodim DB dan `sub` bo'yicha yuklanadi; holat active bo'lishi shart;
 *  3. ruxsat roles.json dan DB dagi ROL bo'yicha olinadi (token `perms` ga ishonilmaydi) —
 *     rol o'zgarishi yoki bloklash darhol kuchga kiradi.
 */
@Injectable()
export class AdminGuard implements CanActivate {
  constructor(
    private readonly reflector: Reflector,
    @Inject(JWT_VERIFIER) private readonly verifier: JwtVerifier,
    @Inject(ROLES) private readonly roles: RolesCatalog,
    @Inject(APP_CONFIG) private readonly cfg: AppConfig,
    private readonly ds: DataSource,
    private readonly audit: AuditService,
  ) {}

  async canActivate(ctx: ExecutionContext): Promise<boolean> {
    const targets = [ctx.getHandler(), ctx.getClass()];
    if (this.reflector.getAllAndOverride<boolean>(PUBLIC_KEY, targets)) return true;

    const req = ctx.switchToHttp().getRequest<AdminRequest>();
    const header = req.headers.authorization;
    const m = typeof header === 'string' ? /^Bearer\s+(\S+)$/i.exec(header) : null;
    if (!m) throw Errors.tokenInvalid();

    let claims;
    try {
      claims = this.verifier.verifyAccess(m[1]);
    } catch (e) {
      if (e instanceof JwtVerifyError) throw e.reason === 'wrong_audience' ? Errors.forbidden() : Errors.tokenInvalid();
      throw e;
    }

    const requestId = (req.headers['x-request-id'] as string | undefined) ?? undefined;
    const staff = await this.loadStaff(claims.sub, claims.role, requestId);
    if (!staff || staff.state !== 'active') throw Errors.forbidden('staff_inactive');
    if (!this.roles.isStaffRole(staff.role)) throw Errors.forbidden();

    const needAuthOnly = this.reflector.getAllAndOverride<boolean>(AUTHED_KEY, targets);
    const perm = this.reflector.getAllAndOverride<string | undefined>(PERM_KEY, targets);
    if (!needAuthOnly) {
      if (!perm || !this.roles.can(staff.role, perm)) throw Errors.forbidden();
    }

    req.staff = staff;
    req.actor = { id: staff.id, role: staff.role, requestId };
    return true;
  }

  private async loadStaff(id: string, tokenRole: string | undefined, requestId?: string): Promise<Staff | null> {
    const repo = this.ds.getRepository(Staff);
    const found = await repo.findOne({ where: { id } });
    if (found) return found;
    // Birinchi (bootstrap) superadmin faqat identity'da yaratiladi. Identity imzolagan tokenda role=superadmin
    // bo'lsa, backoffice yozuvini bir marta yaratamiz (ma'lumot ADMIN_BOOTSTRAP_* dan). Boshqa rollar — yo'q.
    if (tokenRole !== 'superadmin') return null;
    const now = new Date();
    const b = this.cfg.bootstrap;
    const s = repo.create({
      id, email: b.email?.toLowerCase() ?? null, phone: b.phone, fullName: b.name, role: 'superadmin' as StaffRole,
      state: 'active', createdBy: null, createdAt: now, updatedAt: now,
    });
    const attempt = (row: Staff) => this.ds.transaction(async (em) => {
      await em.getRepository(Staff).insert(row);
      await this.audit.record(em, { id, role: 'superadmin', requestId }, {
        action: 'staff.provision', entityType: 'staff', entityId: id, before: null, after: staffSnapshot(row),
      });
    });
    try {
      await attempt(s);
    } catch {
      // email/telefon boshqa yozuvda band bo'lishi yoki parallel so'rov yaratib qo'yishi mumkin:
      // yozuv hali yo'q bo'lsa, kontaktsiz (email/telefon null) yaratamiz
      if (!(await repo.findOne({ where: { id } }))) {
        try { await attempt({ ...s, email: null, phone: null }); } catch { /* quyida null qaytadi */ }
      }
    }
    return repo.findOne({ where: { id } });
  }
}

export function staffSnapshot(s: Staff): Record<string, unknown> {
  return { id: s.id, email: s.email, phone: s.phone, full_name: s.fullName, role: s.role, state: s.state };
}

export const CurrentActor = createParamDecorator((_d: unknown, ctx: ExecutionContext): Actor => {
  return ctx.switchToHttp().getRequest<AdminRequest>().actor as Actor;
});

