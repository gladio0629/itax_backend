import { Controller, Get, Inject, Query } from '@nestjs/common';
import { Transform } from 'class-transformer';
import { IsNotEmpty, IsOptional, IsString, IsUUID, MaxLength } from 'class-validator';
import { DataSource } from 'typeorm';
import { Perm } from '../auth/admin.guard';
import { PagedDateRangeQuery } from '../common/date-query';
import { applyCursor, decodeCursor, DEFAULT_LIMIT, Page, toPage } from '../common/pagination';
import { parseRange } from '../common/time';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { AuditLog } from '../entities/audit-log.entity';

const trim = () => Transform(({ value }) => (typeof value === 'string' ? value.trim() : value));

export class AuditQuery extends PagedDateRangeQuery {
  @IsOptional() @trim() @IsString() @IsNotEmpty() @MaxLength(30)
  entity_type?: string;

  @IsOptional() @trim() @IsString() @IsNotEmpty() @MaxLength(64)
  entity_id?: string;

  @IsOptional() @IsUUID()
  actor_id?: string;

  @IsOptional() @trim() @IsString() @IsNotEmpty() @MaxLength(60)
  action?: string;
}

/** audit_log ustunlari bilan bir xil. actor_id — null (tizim amali), actor_role — xodim roli yoki 'system'. */
export interface AuditEntryView {
  id: string; actor_id: string | null; actor_role: string; action: string; entity_type: string; entity_id: string;
  before: Record<string, unknown> | null; after: Record<string, unknown> | null; request_id: string | null; created_at: string;
}

const toView = (a: AuditLog): AuditEntryView => ({
  id: a.id, actor_id: a.actorId, actor_role: a.actorRole, action: a.action, entity_type: a.entityType,
  entity_id: a.entityId, before: a.before, after: a.after, request_id: a.requestId, created_at: a.createdAt.toISOString(),
});

/** Audit jurnali — faqat o'qish (yozish/o'zgartirish endpointi yo'q; DB trigger ham taqiqlaydi). */
@Controller('admin/v1/audit')
export class AuditController {
  constructor(private readonly ds: DataSource, @Inject(APP_CONFIG) private readonly cfg: AppConfig) {}

  @Get() @Perm('audit:read')
  async list(@Query() q: AuditQuery): Promise<Page<AuditEntryView>> {
    const { from, to } = parseRange(q.from_date, q.to_date, this.cfg.tzOffsetHours);
    const limit = q.limit ?? DEFAULT_LIMIT;
    const qb = this.ds.getRepository(AuditLog).createQueryBuilder('a');
    if (q.entity_type) qb.andWhere('a.entityType = :et', { et: q.entity_type });
    if (q.entity_id) qb.andWhere('a.entityId = :eid', { eid: q.entity_id });
    if (q.actor_id) qb.andWhere('a.actorId = :aid', { aid: q.actor_id.toLowerCase() });
    if (q.action) qb.andWhere('a.action = :act', { act: q.action });
    if (from) qb.andWhere('a.createdAt >= :from', { from });
    if (to) qb.andWhere('a.createdAt < :to', { to });
    applyCursor(qb, 'a', decodeCursor(q.cursor));
    const rows = await qb.orderBy('a.createdAt', 'DESC').addOrderBy('a.id', 'DESC').take(limit + 1).getMany();
    const page = toPage(rows, limit, (r) => r.createdAt);
    return { data: page.data.map(toView), cursor: page.cursor };
  }
}
