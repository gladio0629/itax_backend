import { Inject, Injectable } from '@nestjs/common';
import { DataSource, EntityManager } from 'typeorm';
import { Actor, AuditService } from '../audit/audit.service';
import { staffSnapshot } from '../auth/admin.guard';
import { Errors } from '../common/errors';
import { applyCursor, decodeCursor, DEFAULT_LIMIT, Page, toPage } from '../common/pagination';
import { Staff, StaffState } from '../entities/staff.entity';
import { IdentityStaffPort } from '../ports/ports';
import { ROLES, RolesCatalog, StaffRole } from '../roles/roles.catalog';
import { StaffCreateDto, StaffListQuery, StaffUpdateDto } from './staff.dto';

export interface StaffView {
  id: string; email: string | null; phone: string | null; full_name: string; role: StaffRole;
  state: StaffState; created_at: string;
}

export interface AdminMeView {
  id: string; email: string | null; full_name: string; role: StaffRole; sections: string[];
  permissions: string[]; creatable_roles: StaffRole[];
}

const toView = (s: Staff): StaffView => ({
  id: s.id, email: s.email, phone: s.phone, full_name: s.fullName, role: s.role, state: s.state,
  created_at: s.createdAt.toISOString(),
});

const isUnique = (e: unknown): boolean => {
  const err = e as { code?: string; message?: string };
  return err?.code === '23505' || /unique|duplicate/i.test(err?.message ?? '');
};

@Injectable()
export class StaffService {
  constructor(
    private readonly ds: DataSource,
    private readonly audit: AuditService,
    private readonly identity: IdentityStaffPort,
    @Inject(ROLES) private readonly roles: RolesCatalog,
  ) {}

  async me(actor: Actor): Promise<AdminMeView> {
    const s = await this.ds.getRepository(Staff).findOneOrFail({ where: { id: actor.id! } });
    return {
      id: s.id, email: s.email, full_name: s.fullName, role: s.role,
      sections: this.roles.sectionsOf(s.role),
      permissions: this.roles.permissions(s.role),
      creatable_roles: this.roles.creatable(s.role),
    };
  }

  /** Ko'rinadigan rollar: o'zi yarata oladigan rollar + o'zining roli. Yuqoridagi rollar ko'rinmaydi. */
  private visibleRoles(actor: Actor): StaffRole[] {
    const r = actor.role as StaffRole;
    return [...new Set([...this.roles.creatable(r), r])];
  }

  async list(actor: Actor, q: StaffListQuery): Promise<Page<StaffView>> {
    const limit = q.limit ?? DEFAULT_LIMIT;
    const visible = this.visibleRoles(actor);
    const roles = q.role ? visible.filter((r) => r === q.role) : visible;
    if (roles.length === 0) return { data: [], cursor: null };

    const qb = this.ds.getRepository(Staff).createQueryBuilder('s').where('s.role IN (:...roles)', { roles });
    if (q.state) qb.andWhere('s.state = :state', { state: q.state });
    if (q.q) {
      qb.andWhere('(LOWER(s.fullName) LIKE :q OR LOWER(s.email) LIKE :q OR s.phone LIKE :q)', {
        q: `%${q.q.toLowerCase().replace(/[%_\\]/g, (c) => `\\${c}`)}%`,
      });
    }
    applyCursor(qb, 's', decodeCursor(q.cursor));
    const rows = await qb.orderBy('s.createdAt', 'DESC').addOrderBy('s.id', 'DESC').take(limit + 1).getMany();
    const page = toPage(rows, limit, (r) => r.createdAt);
    return { data: page.data.map(toView), cursor: page.cursor };
  }

  async get(actor: Actor, id: string): Promise<StaffView> {
    const s = await this.ds.getRepository(Staff).findOne({ where: { id } });
    // Ko'rinmaydigan rol — mavjudlik oshkor bo'lmasin
    if (!s || !this.visibleRoles(actor).includes(s.role)) throw Errors.staffNotFound(id);
    return toView(s);
  }

  async create(actor: Actor, dto: StaffCreateDto): Promise<StaffView> {
    const role = dto.role as StaffRole;
    if (!this.roles.creatable(actor.role as StaffRole).includes(role)) throw Errors.forbidden('role_not_creatable');
    if (role !== 'operator' && !dto.email) throw Errors.validation({ email: 'required' });

    const email = dto.email ? dto.email.toLowerCase() : null;
    // Avval o'z bazamizda band emasligini tekshiramiz (identity'da yetim yozuv qolmasligi uchun)
    const repo = this.ds.getRepository(Staff);
    if (email && (await repo.findOne({ where: { email } }))) throw Errors.staffEmailTaken();
    if (await repo.findOne({ where: { phone: dto.phone } })) throw Errors.staffPhoneTaken();

    // Identity xodimni yaratadi va ID ni beradi (token `sub`). Javob bermasa/xato bersa — xodim yaratilmaydi
    // (409 — band email/telefon, 502 — identity ishlamayapti).
    const { id } = await this.identity.create({ phone: dto.phone, email, role, name: dto.full_name });

    const now = new Date();
    const s = repo.create({
      id, email, phone: dto.phone, fullName: dto.full_name, role, state: 'active',
      createdBy: actor.id, createdAt: now, updatedAt: now,
    });
    try {
      await this.ds.transaction(async (em) => {
        await em.getRepository(Staff).insert(s);
        await this.audit.record(em, actor, {
          action: 'staff.create', entityType: 'staff', entityId: s.id, before: null, after: staffSnapshot(s),
        });
      });
    } catch (e) {
      throw await this.mapUnique(e, s.email, s.phone);
    }
    return toView(s);
  }

  async update(actor: Actor, id: string, dto: StaffUpdateDto): Promise<StaffView> {
    const patch = Object.fromEntries(Object.entries(dto).filter(([, v]) => v !== undefined));
    if (Object.keys(patch).length === 0) throw Errors.validation({ body: 'empty' });
    const self = id === actor.id;
    if (self && (dto.role !== undefined || dto.state !== undefined)) throw Errors.forbidden('self_change');

    try {
      return await this.ds.transaction(async (em) => {
        const s = await this.loadManageable(em, actor, id);
        if (dto.role !== undefined && !this.roles.creatable(actor.role as StaffRole).includes(dto.role as StaffRole)) {
          throw Errors.forbidden('role_not_creatable');
        }
        if (dto.role === 'operator' ? false : dto.role !== undefined && !s.email) {
          throw Errors.validation({ email: 'required' }); // operator -> boshqa rol: email bo'lishi shart
        }
        const before = staffSnapshot(s);
        const leavesActiveSuperadmin = s.role === 'superadmin' && s.state === 'active'
          && ((dto.role !== undefined && dto.role !== 'superadmin') || (dto.state !== undefined && dto.state !== 'active'));
        if (leavesActiveSuperadmin) await this.assertNotLastSuperadmin(em, s.id);

        if (dto.phone !== undefined) s.phone = dto.phone;
        if (dto.full_name !== undefined) s.fullName = dto.full_name;
        if (dto.role !== undefined) s.role = dto.role as StaffRole;
        if (dto.state !== undefined) s.state = dto.state; // archived -> active: qaytarish
        s.updatedAt = new Date();
        await em.getRepository(Staff).save(s);
        await this.syncIdentity(s);
        await this.audit.record(em, actor, {
          action: 'staff.update', entityType: 'staff', entityId: s.id, before, after: staffSnapshot(s),
        });
        return toView(s);
      });
    } catch (e) {
      throw await this.mapUnique(e, null, dto.phone ?? null);
    }
  }

  /** DELETE = arxivlash (soft delete). Qaytarish: PUT {state: "active"}. Haqiqiy o'chirish yo'q. */
  async archive(actor: Actor, id: string): Promise<void> {
    if (id === actor.id) throw Errors.forbidden('self_change');
    await this.ds.transaction(async (em) => {
      const s = await this.loadManageable(em, actor, id);
      if (s.state === 'archived') return; // idempotent
      if (s.role === 'superadmin' && s.state === 'active') await this.assertNotLastSuperadmin(em, s.id);
      const before = staffSnapshot(s);
      s.state = 'archived';
      s.updatedAt = new Date();
      await em.getRepository(Staff).save(s);
      await this.syncIdentity(s);
      await this.audit.record(em, actor, {
        action: 'staff.archive', entityType: 'staff', entityId: s.id, before, after: staffSnapshot(s),
      });
    });
  }

  /** Tahrirlash/arxivlash: maqsad xodimning joriy roli yaratuvchining creatable_roles ida bo'lishi shart. */
  private async loadManageable(em: EntityManager, actor: Actor, id: string): Promise<Staff> {
    const s = await em.getRepository(Staff).findOne({ where: { id } });
    if (!s || !this.visibleRoles(actor).includes(s.role)) throw Errors.staffNotFound(id);
    if (!this.roles.creatable(actor.role as StaffRole).includes(s.role)) throw Errors.forbidden('role_not_manageable');
    return s;
  }

  private async assertNotLastSuperadmin(em: EntityManager, excludeId: string): Promise<void> {
    const others = await em.getRepository(Staff).createQueryBuilder('s')
      .where("s.role = 'superadmin' AND s.state = 'active' AND s.id <> :id", { id: excludeId }).getCount();
    if (others === 0) throw Errors.forbidden('last_superadmin');
  }

  private syncIdentity(s: Staff): Promise<void> {
    return this.identity.update({
      id: s.id, email: s.email, phone: s.phone, fullName: s.fullName, role: s.role, active: s.state === 'active',
    });
  }

  private async mapUnique(e: unknown, email: string | null, phone: string | null): Promise<unknown> {
    if (!isUnique(e)) return e;
    // PostgreSQL xatosi constraint nomini beradi; bo'lmasa (yoki ishonchsiz bo'lsa) qiymat bo'yicha tekshiramiz
    const c = (e as { constraint?: string; driverError?: { constraint?: string } });
    const name = c.constraint ?? c.driverError?.constraint;
    if (name === 'uq_staff_email') return Errors.staffEmailTaken();
    if (name === 'uq_staff_phone') return Errors.staffPhoneTaken();
    const repo = this.ds.getRepository(Staff);
    if (email && (await repo.findOne({ where: { email } }))) return Errors.staffEmailTaken();
    if (phone && (await repo.findOne({ where: { phone } }))) return Errors.staffPhoneTaken();
    return e;
  }
}
