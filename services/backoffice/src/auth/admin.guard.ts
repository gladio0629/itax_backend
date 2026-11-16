import { CanActivate, ExecutionContext, Inject, Injectable, Logger, SetMetadata, createParamDecorator } from '@nestjs/common';
import { Reflector } from '@nestjs/core';
import { DataSource } from 'typeorm';
import type { Request } from 'express';
import { Actor, AuditService } from '../audit/audit.service';
import { AppError, Errors, isUniqueViolation } from '../common/errors';
import { Staff } from '../entities/staff.entity';
import { IdentityStaffPort } from '../ports/ports';
import { ROLES, RolesCatalog, StaffRole } from '../roles/roles.catalog';
import { JwtVerifier, JwtVerifyError } from './jwt.verifier';
import { SessionRevocationPort } from './session-revocation';

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
 *     sessiya bekor qilinmaganligi (logout: Redis `bl:sid:<sid>`) tekshiriladi — Redis ishlamasa rad etiladi;
 *  2. xodim DB dan `sub` bo'yicha yuklanadi; holat active bo'lishi shart;
 *  3. ruxsat roles.json dan DB dagi ROL bo'yicha olinadi (token `perms` ga ishonilmaydi) —
 *     rol o'zgarishi yoki bloklash darhol kuchga kiradi.
 */
@Injectable()
export class AdminGuard implements CanActivate {
  private readonly log = new Logger(AdminGuard.name);

  constructor(
    private readonly reflector: Reflector,
    @Inject(JWT_VERIFIER) private readonly verifier: JwtVerifier,
    @Inject(ROLES) private readonly roles: RolesCatalog,
    private readonly ds: DataSource,
    private readonly audit: AuditService,
    private readonly sessions: SessionRevocationPort,
    private readonly identity: IdentityStaffPort,
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

    // Logout qilingan sessiya — yaroqsiz token bilan bir xil 401. Tekshiruv staff yuklash (va superadmin
    // bootstrap) dan OLDIN. Redis xatosi — fail closed: xato yuqoriga ketadi (500), so'rov o'tkazilmaydi.
    let revoked: boolean;
    try {
      revoked = await this.sessions.isRevoked(claims.sid);
    } catch (e) {
      this.log.error(`sessiya bekor qilinganini tekshirib bo'lmadi: ${(e as Error).message}`);
      throw new AppError(500, 'INTERNAL_ERROR', { reason: 'session_check_unavailable' });
    }
    if (revoked) throw Errors.tokenInvalid();

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
    // Superadmin (bootstrap yoki seed-superadmin.sh) faqat identity'da yaratiladi. Tokenda role=superadmin bo'lsa,
    // backoffice yozuvini bir marta yaratamiz — email/telefon/ism AYNAN shu akkauntniki, identity'dan
    // (GET /internal/identity/staff/{id}). Boshqa rollar — yo'q (ular POST /admin/v1/staff orqali yaratiladi).
    if (tokenRole !== 'superadmin') return null;
    // Identity javob bermasa — 502, yozuv yaratilmaydi (keyingi so'rovda qayta urinadi)
    const ident = await this.identity.get(id);
    if (!ident || ident.role !== 'superadmin' || !ident.active) {
      this.log.warn(`superadmin tokeni (${id}) identity'da aktiv superadmin emas — backoffice yozuvi yaratilmadi`);
      return null;
    }
    const now = new Date();
    const s = repo.create({
      id, email: ident.email?.toLowerCase() ?? null, phone: ident.phone, fullName: ident.fullName,
      role: 'superadmin' as StaffRole, state: 'active', createdBy: null, createdAt: now, updatedAt: now,
    });
    const attempt = (row: Staff) => this.ds.transaction(async (em) => {
      await em.getRepository(Staff).insert(row);
      await this.audit.record(em, { id, role: 'superadmin', requestId }, {
        action: 'staff.provision', entityType: 'staff', entityId: id, before: null, after: staffSnapshot(row),
      });
    });
    try {
      await attempt(s);
    } catch (e) {
      // Parallel so'rov shu yozuvni yaratib qo'ygan bo'lishi mumkin
      const raced = await repo.findOne({ where: { id } });
      if (raced) return raced;
      if (!isUniqueViolation(e)) throw e;
      // Email/telefon backoffice'dagi BOSHQA yozuvda band (ma'lumotlar identity bilan mos emas): kirishni
      // to'smaymiz — kontaktsiz yaratamiz, ziddiyatni logga yozamiz (qo'lda tuzatish kerak)
      this.log.warn(`superadmin ${id}: email/telefon backoffice'dagi boshqa xodimda band — kontaktsiz yaratildi`);
      try {
        await attempt({ ...s, email: null, phone: null });
      } catch (e2) {
        if (!(await repo.findOne({ where: { id } }))) throw e2;
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

