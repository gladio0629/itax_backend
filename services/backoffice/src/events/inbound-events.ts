import { Inject, Injectable } from '@nestjs/common';
import { DataSource } from 'typeorm';
import { AuditService } from '../audit/audit.service';
import { isUuid } from '../common/pipes';
import { Staff } from '../entities/staff.entity';
import { OperatorService } from '../operator/operator.service';
import { ROLES, RolesCatalog } from '../roles/roles.catalog';

const LOGIN_METHODS = ['email', 'sms'];

/** Kiruvchi hodisalar. Idempotent: bir hodisa ikki marta kelsa navbatga/auditga ikki marta tushmaydi. */
@Injectable()
export class InboundEvents {
  constructor(
    private readonly operator: OperatorService,
    private readonly ds: DataSource,
    private readonly audit: AuditService,
    @Inject(ROLES) private readonly roles: RolesCatalog,
  ) {}

  /** driver: driver.submitted -> operator navbatiga "driver_application". */
  async onDriverSubmitted(env: unknown): Promise<void> {
    const e = env as { event_id?: unknown; payload?: { driver_id?: unknown } };
    if (!isUuid(e?.event_id) || !isUuid(e?.payload?.driver_id)) throw new SyntaxError('driver.submitted: noto\'g\'ri format');
    await this.operator.enqueueDriverApplication(e.event_id, e.payload.driver_id);
  }

  /** identity: staff.logged_in -> audit `auth.login` (kim, qachon, qaysi sessiya, qaysi usul). */
  async onStaffLoggedIn(env: unknown): Promise<void> {
    const e = this.parseSession(env, 'staff.logged_in', 'logged_in_at');
    const method = (env as { payload: { method?: unknown } }).payload.method;
    if (typeof method !== 'string' || !LOGIN_METHODS.includes(method)) throw new SyntaxError('staff.logged_in: method');
    await this.recordSession(e, 'auth.login', { session_id: e.sessionId, method });
  }

  /** identity: staff.logged_out -> audit `auth.logout`. */
  async onStaffLoggedOut(env: unknown): Promise<void> {
    const e = this.parseSession(env, 'staff.logged_out', 'logged_out_at');
    await this.recordSession(e, 'auth.logout', { session_id: e.sessionId });
  }

  private parseSession(env: unknown, name: string, atField: string) {
    const e = env as { event?: unknown; event_id?: unknown; trace_id?: unknown; payload?: Record<string, unknown> };
    const p = e?.payload;
    const sid = p?.session_id;
    const at = typeof p?.[atField] === 'string' ? new Date(p[atField] as string) : null;
    if (
      e?.event !== name || !isUuid(e.event_id) || !p || !isUuid(p.staff_id) || !this.roles.isStaffRole(p.role)
      || typeof sid !== 'string' || sid.length === 0 || sid.length > 64 || !at || Number.isNaN(at.getTime())
    ) {
      throw new SyntaxError(`${name}: noto'g'ri format`);
    }
    return {
      eventId: e.event_id.toLowerCase(), staffId: p.staff_id.toLowerCase(), role: p.role, sessionId: sid, at,
      traceId: typeof e.trace_id === 'string' ? e.trace_id.slice(0, 100) : undefined,
    };
  }

  private async recordSession(
    e: ReturnType<InboundEvents['parseSession']>, action: string, after: Record<string, unknown>,
  ): Promise<void> {
    // Rol — backoffice'dagi joriy yozuvdan (bo'lmasa — hodisadagi). Telefon/email auditga yozilmaydi.
    const staff = await this.ds.getRepository(Staff).findOne({ where: { id: e.staffId } });
    await this.audit.recordOnce(this.ds.manager, e.eventId, {
      id: e.staffId, role: staff?.role ?? e.role, requestId: e.traceId,
    }, { action, entityType: 'staff_session', entityId: e.sessionId, before: null, after }, e.at);
  }
}
