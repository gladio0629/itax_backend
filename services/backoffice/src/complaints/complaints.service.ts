import { Injectable } from '@nestjs/common';
import { DataSource, EntityManager } from 'typeorm';
import { v4 as uuidv4 } from 'uuid';
import { Actor, AuditService } from '../audit/audit.service';
import { Errors } from '../common/errors';
import { applyCursor, decodeCursor, DEFAULT_LIMIT, Page, toPage } from '../common/pagination';
import { Complaint, ComplaintNote, ComplaintStatus } from '../entities/complaint.entity';
import { Staff } from '../entities/staff.entity';
import {
  ComplaintAssignDto, ComplaintCreateDto, ComplaintListQuery, ComplaintNoteDto, ComplaintStatusDto,
} from './complaints.dto';

export interface ComplaintView {
  id: string; order_id: string | null; subject_type: string; subject_id: string; category: string; description: string;
  status: ComplaintStatus; assigned_to: string | null; resolution: string | null; created_by: string;
  created_at: string; updated_at: string;
}
export interface ComplaintNoteView { id: string; complaint_id: string; author_id: string; text: string; created_at: string }
export interface ComplaintDetailView extends ComplaintView { notes: ComplaintNoteView[] }

const toView = (c: Complaint): ComplaintView => ({
  id: c.id, order_id: c.orderId, subject_type: c.subjectType, subject_id: c.subjectId, category: c.category,
  description: c.description, status: c.status, assigned_to: c.assignedTo, resolution: c.resolution,
  created_by: c.createdBy, created_at: c.createdAt.toISOString(), updated_at: c.updatedAt.toISOString(),
});
const noteView = (n: ComplaintNote): ComplaintNoteView => ({
  id: n.id, complaint_id: n.complaintId, author_id: n.authorId, text: n.text, created_at: n.createdAt.toISOString(),
});
/** Audit uchun qisqa nusxa (tavsif matnisiz) */
const snap = (c: Complaint): Record<string, unknown> => ({
  id: c.id, order_id: c.orderId, subject_type: c.subjectType, subject_id: c.subjectId, category: c.category,
  status: c.status, assigned_to: c.assignedTo, resolution: c.resolution,
});

/** Ruxsat etilgan holat o'tishlari. resolved / rejected — yakuniy. */
const TRANSITIONS: Record<ComplaintStatus, ComplaintStatus[]> = {
  open: ['in_progress', 'rejected'],
  in_progress: ['resolved', 'rejected'],
  resolved: [],
  rejected: [],
};
const isFinal = (s: ComplaintStatus) => TRANSITIONS[s].length === 0;

/**
 * Shikoyatlar. Har bir yozish amali audit bilan bir tranzaksiyada.
 * Holat o'zgarishi — shartli UPDATE (WHERE status = <o'qilgan holat>): parallel so'rovlarda faqat bittasi o'tadi.
 */
@Injectable()
export class ComplaintsService {
  constructor(private readonly ds: DataSource, private readonly audit: AuditService) {}

  async list(q: ComplaintListQuery): Promise<Page<ComplaintView>> {
    const limit = q.limit ?? DEFAULT_LIMIT;
    const qb = this.ds.getRepository(Complaint).createQueryBuilder('c');
    if (q.status) qb.andWhere('c.status = :st', { st: q.status });
    if (q.assigned_to) qb.andWhere('c.assignedTo = :as', { as: q.assigned_to.toLowerCase() });
    if (q.subject_type) qb.andWhere('c.subjectType = :sub', { sub: q.subject_type });
    applyCursor(qb, 'c', decodeCursor(q.cursor));
    const rows = await qb.orderBy('c.createdAt', 'DESC').addOrderBy('c.id', 'DESC').take(limit + 1).getMany();
    const page = toPage(rows, limit, (r) => r.createdAt);
    return { data: page.data.map(toView), cursor: page.cursor };
  }

  async get(id: string): Promise<ComplaintDetailView> {
    const c = await this.load(this.ds.manager, id);
    const notes = await this.ds.getRepository(ComplaintNote).find({
      where: { complaintId: id }, order: { createdAt: 'ASC', id: 'ASC' },
    });
    return { ...toView(c), notes: notes.map(noteView) };
  }

  async create(actor: Actor, dto: ComplaintCreateDto): Promise<ComplaintView> {
    const now = new Date();
    const c = this.ds.getRepository(Complaint).create({
      id: uuidv4(), orderId: dto.order_id?.toLowerCase() ?? null, subjectType: dto.subject_type,
      subjectId: dto.subject_id.toLowerCase(), category: dto.category, description: dto.description, status: 'open',
      assignedTo: null, resolution: null, createdBy: actor.id!, createdAt: now, updatedAt: now,
    });
    await this.ds.transaction(async (em) => {
      await em.getRepository(Complaint).insert(c);
      await this.audit.record(em, actor, { action: 'complaint.create', entityType: 'complaint', entityId: c.id, before: null, after: snap(c) });
    });
    return toView(c);
  }

  /** Mas'ul xodim tayinlash (qayta tayinlash ham). open → in_progress. Yakunlangan shikoyat — 403. */
  async assign(actor: Actor, id: string, dto: ComplaintAssignDto): Promise<ComplaintView> {
    const staffId = dto.staff_id.toLowerCase();
    const staff = await this.ds.getRepository(Staff).findOne({ where: { id: staffId } });
    if (!staff || staff.state !== 'active') throw Errors.validation({ staff_id: staff ? 'inactive' : 'not_found' });
    return this.change(actor, id, 'complaint.assign', (cur) => {
      if (isFinal(cur.status)) throw Errors.forbidden('complaint_closed');
      return { assignedTo: staffId, status: cur.status === 'open' ? 'in_progress' : cur.status };
    });
  }

  async setStatus(actor: Actor, id: string, dto: ComplaintStatusDto): Promise<ComplaintView> {
    const final = dto.status === 'resolved' || dto.status === 'rejected';
    if (final && !dto.resolution) throw Errors.validation({ resolution: 'required' });
    if (!final && dto.resolution !== undefined) throw Errors.validation({ resolution: 'not_allowed' });
    return this.change(actor, id, `complaint.${dto.status === 'in_progress' ? 'start' : dto.status === 'resolved' ? 'resolve' : 'reject'}`, (cur) => {
      if (!TRANSITIONS[cur.status].includes(dto.status)) throw Errors.forbidden('invalid_transition');
      return {
        status: dto.status,
        // ishga olishda mas'ul bo'lmasa — amalni bajargan xodim mas'ul bo'ladi
        ...(dto.status === 'in_progress' && !cur.assignedTo ? { assignedTo: actor.id } : {}),
        ...(final ? { resolution: dto.resolution! } : {}),
      };
    });
  }

  async addNote(actor: Actor, id: string, dto: ComplaintNoteDto): Promise<ComplaintNoteView> {
    const n = this.ds.getRepository(ComplaintNote).create({
      id: uuidv4(), complaintId: id, authorId: actor.id!, text: dto.text, createdAt: new Date(),
    });
    await this.ds.transaction(async (em) => {
      await this.load(em, id);
      await em.getRepository(ComplaintNote).insert(n);
      await this.audit.record(em, actor, {
        action: 'complaint.note.add', entityType: 'complaint', entityId: id,
        before: null, after: { note_id: n.id, author_id: n.authorId },
      });
    });
    return noteView(n);
  }

  private async load(em: EntityManager, id: string): Promise<Complaint> {
    const c = await em.getRepository(Complaint).findOne({ where: { id } });
    if (!c) throw Errors.notFound(id);
    return c;
  }

  /**
   * O'qish -> qoida -> shartli UPDATE (status o'qilgandek bo'lsa) -> audit; hammasi bitta tranzaksiyada.
   * Parallel o'zgarish bo'lsa (affected=0) qoida yangi holat bo'yicha qayta tekshiriladi (bir marta).
   */
  private async change(
    actor: Actor, id: string, action: string,
    rule: (cur: Complaint) => Partial<Pick<Complaint, 'status' | 'assignedTo' | 'resolution'>>,
  ): Promise<ComplaintView> {
    for (let attempt = 0; ; attempt++) {
      const res = await this.ds.transaction(async (em) => {
        const cur = await this.load(em, id);
        const patch = rule(cur);
        const r = await em.getRepository(Complaint).createQueryBuilder().update()
          .set({ ...patch, updatedAt: new Date() })
          .where('id = :id AND status = :st', { id, st: cur.status })
          .execute();
        if ((r.affected ?? 0) !== 1) return null;
        const after = await this.load(em, id);
        await this.audit.record(em, actor, { action, entityType: 'complaint', entityId: id, before: snap(cur), after: snap(after) });
        return after;
      });
      if (res) return toView(res);
      if (attempt >= 1) throw Errors.forbidden('invalid_transition');
    }
  }
}
