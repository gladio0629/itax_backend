import { Injectable } from '@nestjs/common';
import { DataSource, EntityManager } from 'typeorm';
import { v4 as uuidv4 } from 'uuid';
import { Actor, AuditService } from '../audit/audit.service';
import { Errors } from '../common/errors';
import { applyCursor, decodeCursor, DEFAULT_LIMIT, Page, PageQuery, toPage } from '../common/pagination';
import { PendingChange, Tariff, TariffValues } from '../entities/tariff.entity';
import { TariffInputDto } from './tariffs.dto';

export interface TariffView extends TariffValues {
  id: string; active: boolean; approval_status: string; pending_change: PendingChange | null; updated_at: string;
}

const values = (t: Tariff): TariffValues => ({
  name: t.name, car_class: t.carClass, base_fare: t.baseFare, per_km: t.perKm, per_min: t.perMin,
  min_fare: t.minFare, commission_percent: t.commissionPercent,
});
const toView = (t: Tariff): TariffView => ({
  id: t.id, ...values(t), active: t.active, approval_status: t.approvalStatus,
  pending_change: t.pendingChange, updated_at: t.updatedAt.toISOString(),
});
const snap = (t: Tariff): Record<string, unknown> => ({
  ...values(t), id: t.id, active: t.active, approval_status: t.approvalStatus,
  pending_change: t.pendingChange, deleted: t.deletedAt !== null,
});
const toValues = (d: TariffInputDto): TariffValues => ({
  name: d.name, car_class: d.car_class, base_fare: d.base_fare, per_km: d.per_km, per_min: d.per_min,
  min_fare: d.min_fare, commission_percent: d.commission_percent,
});

/**
 * Tariflar: har qanday o'zgarish (yaratish/o'zgartirish/o'chirish) pending_approval holatida turadi va
 * ikkinchi xodim tasdiqlagandagina kuchga kiradi. Amaldagi qiymatlar tasdiqqacha o'zgarmaydi.
 */
@Injectable()
export class TariffsService {
  constructor(private readonly ds: DataSource, private readonly audit: AuditService) {}

  async list(q: PageQuery): Promise<Page<TariffView>> {
    const limit = q.limit ?? DEFAULT_LIMIT;
    const qb = this.ds.getRepository(Tariff).createQueryBuilder('t').where('t.deletedAt IS NULL');
    applyCursor(qb, 't', decodeCursor(q.cursor));
    const rows = await qb.orderBy('t.createdAt', 'DESC').addOrderBy('t.id', 'DESC').take(limit + 1).getMany();
    const page = toPage(rows, limit, (r) => r.createdAt);
    return { data: page.data.map(toView), cursor: page.cursor };
  }

  async get(id: string): Promise<TariffView> {
    return toView(await this.mustFind(this.ds.manager, id));
  }

  async create(actor: Actor, dto: TariffInputDto): Promise<TariffView> {
    const now = new Date();
    const t = this.ds.getRepository(Tariff).create({
      id: uuidv4(), ...fromValues(toValues(dto)), active: false, approvalStatus: 'pending_approval',
      pendingChange: { action: 'create', requested_by: actor.id!, requested_at: now.toISOString() },
      deletedAt: null, createdAt: now, updatedAt: now,
    });
    await this.ds.transaction(async (em) => {
      await em.getRepository(Tariff).insert(t as never);
      await this.audit.record(em, actor, { action: 'tariff.create', entityType: 'tariff', entityId: t.id, before: null, after: snap(t) });
    });
    return toView(t);
  }

  async update(actor: Actor, id: string, dto: TariffInputDto): Promise<TariffView> {
    return this.request(actor, id, { action: 'update', values: toValues(dto) });
  }

  async remove(actor: Actor, id: string): Promise<TariffView> {
    return this.request(actor, id, { action: 'delete' });
  }

  private request(actor: Actor, id: string, change: { action: 'update' | 'delete'; values?: TariffValues }): Promise<TariffView> {
    return this.ds.transaction(async (em) => {
      const t = await this.mustFind(em, id);
      const before = snap(t);
      const now = new Date();
      const pending: PendingChange = { action: change.action, requested_by: actor.id!, requested_at: now.toISOString() };
      if (change.values) pending.values = change.values;
      // Faqat kutayotgan o'zgarishi yo'q tarifga (parallel so'rovlarda bittasi yutadi)
      const res = await em.getRepository(Tariff).createQueryBuilder().update()
        .set({ approvalStatus: 'pending_approval', pendingChange: pending as never, updatedAt: now })
        .where("id = :id AND approval_status <> 'pending_approval' AND deleted_at IS NULL", { id }).execute();
      if ((res.affected ?? 0) !== 1) throw Errors.tariffChangePending();
      const after = await this.mustFind(em, id);
      await this.audit.record(em, actor, { action: `tariff.${change.action}`, entityType: 'tariff', entityId: id, before, after: snap(after) });
      return toView(after);
    });
  }

  approve(actor: Actor, id: string): Promise<TariffView> {
    return this.decide(actor, id, null);
  }

  reject(actor: Actor, id: string, reason: string): Promise<TariffView> {
    return this.decide(actor, id, reason);
  }

  private decide(actor: Actor, id: string, rejectReason: string | null): Promise<TariffView> {
    return this.ds.transaction(async (em) => {
      const t = await this.mustFind(em, id);
      const pc = t.pendingChange;
      if (t.approvalStatus !== 'pending_approval' || !pc) throw Errors.forbidden('not_pending');
      if (pc.requested_by === actor.id) throw Errors.forbidden('self_approval');

      const before = snap(t);
      const now = new Date();
      const set: Record<string, unknown> = { pendingChange: null, updatedAt: now };
      if (rejectReason !== null) {
        set.approvalStatus = 'rejected'; // amaldagi qiymatlar o'zgarmaydi
      } else {
        set.approvalStatus = 'approved';
        if (pc.action === 'delete') {
          set.deletedAt = now;
          set.active = false;
        } else {
          set.active = true;
          if (pc.action === 'update' && pc.values) Object.assign(set, fromValues(pc.values));
        }
      }
      // updated_at bo'yicha optimistik tekshiruv: parallel approve/reject dan faqat bittasi o'tadi
      const res = await em.getRepository(Tariff).createQueryBuilder().update().set(set as never)
        .where("id = :id AND approval_status = 'pending_approval' AND updated_at = :ts", { id, ts: t.updatedAt }).execute();
      if ((res.affected ?? 0) !== 1) throw Errors.forbidden('not_pending');

      const after = await this.mustFind(em, id, true);
      await this.audit.record(em, actor, {
        action: rejectReason !== null ? 'tariff.reject' : 'tariff.approve', entityType: 'tariff', entityId: id,
        before, after: { ...snap(after), ...(rejectReason !== null ? { reject_reason: rejectReason } : {}) },
      });
      return toView(after);
    });
  }

  private async mustFind(em: EntityManager, id: string, allowDeleted = false): Promise<Tariff> {
    const t = await em.getRepository(Tariff).findOne({ where: { id } });
    if (!t || (t.deletedAt && !allowDeleted)) throw Errors.tariffNotFound(id);
    return t;
  }
}

function fromValues(v: TariffValues) {
  return {
    name: v.name, carClass: v.car_class, baseFare: v.base_fare, perKm: v.per_km, perMin: v.per_min,
    minFare: v.min_fare, commissionPercent: v.commission_percent,
  };
}
