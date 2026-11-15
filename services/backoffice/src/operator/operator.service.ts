import { Inject, Injectable, Logger } from '@nestjs/common';
import { DataSource, EntityManager } from 'typeorm';
import { v4 as uuidv4 } from 'uuid';
import { Actor, AuditService, SYSTEM_ACTOR } from '../audit/audit.service';
import { Errors } from '../common/errors';
import { applyCursor, decodeCursor, DEFAULT_LIMIT, Page, toPage } from '../common/pagination';
import { parseRange, todayRange } from '../common/time';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { OutboxEvent } from '../entities/outbox.entity';
import { QueueItem } from '../entities/queue-item.entity';
import { driverApprovedEvent } from '../events/envelope';
import { DriverLookupPort, UserLookupPort } from '../ports/ports';
import { ROLES, RolesCatalog, StaffRole } from '../roles/roles.catalog';
import { HistoryQuery, QueueCompleteDto, QueueQuery, QueueRejectDto } from './operator.dto';

export interface QueueItemView {
  id: string; kind: string; subject_id: string; subject_phone: string | null; status: string;
  assigned_to: string | null; result: string | null; comment: string | null; reject_reason: string | null;
  created_at: string; finished_at: string | null;
}

/** +998901234567 -> 998**___**67 (to'liq raqam faqat ishni boshlagan operatorga ko'rinadi) */
export function maskPhone(phone: string | null): string | null {
  if (!phone) return null;
  const d = phone.replace(/\D/g, '');
  if (d.length < 5) return '***';
  return `${d.slice(0, 3)}**___**${d.slice(-2)}`;
}

const snap = (q: QueueItem): Record<string, unknown> => ({
  id: q.id, kind: q.kind, subject_id: q.subjectId, status: q.status, assigned_to: q.assignedTo,
  finished_by: q.finishedBy, result: q.result, comment: q.comment, reject_reason: q.rejectReason,
});

@Injectable()
export class OperatorService {
  private readonly log = new Logger(OperatorService.name);

  constructor(
    private readonly ds: DataSource,
    private readonly audit: AuditService,
    private readonly drivers: DriverLookupPort,
    private readonly users: UserLookupPort,
    @Inject(ROLES) private readonly roles: RolesCatalog,
    @Inject(APP_CONFIG) private readonly cfg: AppConfig,
  ) {}

  // ------------------------------------------------------------------ o'qish
  async queue(actor: Actor, q: QueueQuery): Promise<Page<QueueItemView>> {
    const limit = q.limit ?? DEFAULT_LIMIT;
    const section = q.section ?? 'waiting';
    const senior = this.roles.isSenior(actor.role as StaffRole);
    const qb = this.ds.getRepository(QueueItem).createQueryBuilder('i');
    let asc = false;

    if (section === 'waiting') {
      qb.where("i.status = 'waiting'");
      asc = true; // navbat: eng eskisi birinchi
    } else if (section === 'in_progress') {
      qb.where("i.status = 'in_progress'");
      if (!senior) qb.andWhere('i.assignedTo = :me', { me: actor.id }); // operator faqat o'zinikini
    } else {
      if (!senior) throw Errors.forbidden('section_not_allowed');
      const { from, to } = todayRange(new Date(), this.cfg.tzOffsetHours);
      qb.where("i.status = 'completed' AND i.result = 'resolved' AND i.finishedAt >= :from AND i.finishedAt < :to", { from, to });
    }

    applyCursor(qb, 'i', decodeCursor(q.cursor), 'createdAt', asc);
    const dir = asc ? 'ASC' : 'DESC';
    const rows = await qb.orderBy('i.createdAt', dir).addOrderBy('i.id', dir).take(limit + 1).getMany();
    const page = toPage(rows, limit, (r) => r.createdAt);
    return { data: await Promise.all(page.data.map((r) => this.view(r, actor))), cursor: page.cursor };
  }

  /** Operator o'z tarixi: o'zi yakunlagan yoki rad etgan ishlar. */
  async history(actor: Actor, q: HistoryQuery): Promise<Page<QueueItemView>> {
    const limit = q.limit ?? DEFAULT_LIMIT;
    const { from, to } = parseRange(q.from_date, q.to_date, this.cfg.tzOffsetHours);
    const qb = this.ds.getRepository(QueueItem).createQueryBuilder('i')
      .where('i.finishedBy = :me', { me: actor.id })
      .andWhere("i.status IN ('completed', 'rejected')");
    if (q.result === 'rejected') qb.andWhere("i.status = 'rejected'");
    else if (q.result) qb.andWhere('i.result = :res', { res: q.result });
    if (from) qb.andWhere('i.finishedAt >= :from', { from });
    if (to) qb.andWhere('i.finishedAt < :to', { to });
    applyCursor(qb, 'i', decodeCursor(q.cursor));
    const rows = await qb.orderBy('i.createdAt', 'DESC').addOrderBy('i.id', 'DESC').take(limit + 1).getMany();
    const page = toPage(rows, limit, (r) => r.createdAt);
    return { data: await Promise.all(page.data.map((r) => this.view(r, actor))), cursor: page.cursor };
  }

  // ----------------------------------------------------------------- amallar
  async start(actor: Actor, id: string): Promise<QueueItemView> {
    return this.ds.transaction(async (em) => {
      // Shartli UPDATE: ikki operator bir vaqtda boshlasa, faqat bittasi yutadi
      const res = await em.getRepository(QueueItem).createQueryBuilder().update()
        .set({ status: 'in_progress', assignedTo: actor.id! })
        .where("id = :id AND status = 'waiting'", { id }).execute();
      const item = await this.mustFind(em, id);
      if ((res.affected ?? 0) !== 1) throw Errors.forbidden('not_waiting');
      await this.audit.record(em, actor, {
        action: 'operator.start', entityType: 'queue_item', entityId: id,
        before: { status: 'waiting', assigned_to: null }, after: snap(item),
      });
      return this.view(item, actor);
    });
  }

  async reject(actor: Actor, id: string, dto: QueueRejectDto): Promise<QueueItemView> {
    return this.ds.transaction(async (em) => {
      const res = await em.getRepository(QueueItem).createQueryBuilder().update()
        .set({ status: 'rejected', rejectReason: dto.reason, finishedBy: actor.id!, finishedAt: new Date() })
        .where("id = :id AND status = 'waiting'", { id }).execute();
      const item = await this.mustFind(em, id);
      if ((res.affected ?? 0) !== 1) throw Errors.forbidden('not_waiting');
      await this.audit.record(em, actor, {
        action: 'operator.reject', entityType: 'queue_item', entityId: id, before: { status: 'waiting' }, after: snap(item),
      });
      return this.view(item, actor);
    });
  }

  async complete(actor: Actor, id: string, dto: QueueCompleteDto): Promise<QueueItemView> {
    return this.ds.transaction(async (em) => {
      const res = await em.getRepository(QueueItem).createQueryBuilder().update()
        .set({
          status: 'completed', result: dto.result, comment: dto.comment ?? null,
          finishedBy: actor.id!, finishedAt: new Date(),
        })
        .where("id = :id AND status = 'in_progress' AND assigned_to = :me", { id, me: actor.id }).execute();
      const item = await this.mustFind(em, id);
      if ((res.affected ?? 0) !== 1) {
        throw Errors.forbidden(item.status === 'in_progress' ? 'not_assigned' : 'not_in_progress');
      }
      await this.audit.record(em, actor, {
        action: 'operator.complete', entityType: 'queue_item', entityId: id,
        before: { status: 'in_progress', assigned_to: actor.id }, after: snap(item),
      });
      // Haydovchi arizasi "resolved" — driver.approved (producer: backoffice, shared/events/driver.approved.json)
      if (item.kind === 'driver_application' && dto.result === 'resolved' && this.cfg.publishApprovedOnResolved) {
        const ev = driverApprovedEvent({ driverId: item.subjectId, approvedBy: actor.id!, approvedAt: new Date() }, actor.requestId);
        await em.getRepository(OutboxEvent).insert({ ...ev, createdAt: new Date(), publishedAt: null } as never);
      }
      return this.view(item, actor);
    });
  }

  // ------------------------------------------------------------ hodisalardan
  /** driver.submitted -> navbatga yangi ish. event_id bo'yicha idempotent. */
  async enqueueDriverApplication(eventId: string, driverId: string): Promise<boolean> {
    try {
      return await this.ds.transaction(async (em) => {
        const dup = await em.getRepository(QueueItem).findOne({ where: { sourceEventId: eventId } });
        if (dup) return false;
        const item = em.getRepository(QueueItem).create({
          id: uuidv4(), kind: 'driver_application', subjectId: driverId, status: 'waiting', assignedTo: null,
          finishedBy: null, result: null, comment: null, rejectReason: null, sourceEventId: eventId,
          createdAt: new Date(), finishedAt: null,
        });
        await em.getRepository(QueueItem).insert(item);
        await this.audit.record(em, SYSTEM_ACTOR, {
          action: 'operator.enqueue', entityType: 'queue_item', entityId: item.id, before: null, after: snap(item),
        });
        return true;
      });
    } catch (e) {
      if (/unique|duplicate/i.test((e as Error).message)) return false; // parallel dublikat
      throw e;
    }
  }

  // ---------------------------------------------------------------- yordamchi
  private async mustFind(em: EntityManager, id: string): Promise<QueueItem> {
    const item = await em.getRepository(QueueItem).findOne({ where: { id } });
    if (!item) throw Errors.notFound(id);
    return item;
  }

  private async view(i: QueueItem, actor: Actor): Promise<QueueItemView> {
    const phone = await this.phoneOf(i);
    const showFull = i.status === 'in_progress' && i.assignedTo === actor.id;
    return {
      id: i.id, kind: i.kind, subject_id: i.subjectId,
      subject_phone: showFull ? phone : maskPhone(phone),
      status: i.status, assigned_to: i.assignedTo, result: i.result, comment: i.comment,
      reject_reason: i.rejectReason, created_at: i.createdAt.toISOString(), finished_at: i.finishedAt?.toISOString() ?? null,
    };
  }

  private async phoneOf(i: QueueItem): Promise<string | null> {
    try {
      const d = await this.drivers.getDriver(i.subjectId);
      if (d) return d.phone;
      if (i.kind === 'complaint') return (await this.users.getUser(i.subjectId))?.phone ?? null;
    } catch (e) {
      this.log.warn(`telefonni olib bo'lmadi (${i.subjectId}): ${(e as Error).message}`);
    }
    return null;
  }
}
