import { Body, Controller, Get, HttpCode, Inject, Param, Patch, Post, Query } from '@nestjs/common';
import { DataSource } from 'typeorm';
import { Actor, AuditService } from '../audit/audit.service';
import { CurrentActor, Perm } from '../auth/admin.guard';
import { Errors } from '../common/errors';
import type { Page } from '../common/pagination';
import { UuidParamPipe } from '../common/pipes';
import { parseRange } from '../common/time';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { OrderPatch, OrdersPort, OrderView } from '../ports/ports';
import { AdminOrderCancelDto, AdminOrderUpdateDto, OrderListQuery } from './orders.dto';

/** Audit uchun: tahrirlanadigan maydonlar va holat */
const snap = (o: OrderView): Record<string, unknown> => ({
  id: o.id, status: o.status, comment: o.comment ?? null, payment_method: o.payment_method ?? null,
  scheduled_at: o.scheduled_at ?? null, scheduled_from: o.scheduled_from ?? null, scheduled_to: o.scheduled_to ?? null,
  cancel_reason: o.cancel_reason ?? null,
});

/** PATCH tanasining o'zaro qoidalari (class-validator dan keyin) */
function checkPatch(dto: AdminOrderUpdateDto): OrderPatch {
  const patch: OrderPatch = Object.fromEntries(Object.entries(dto).filter(([, v]) => v !== undefined));
  if (Object.keys(patch).length === 0) throw Errors.validation({ body: 'empty' });
  const fields: Record<string, string> = {};
  const hasFrom = patch.scheduled_from !== undefined;
  const hasTo = patch.scheduled_to !== undefined;
  if (patch.scheduled_at !== undefined && (hasFrom || hasTo)) fields.scheduled_at = 'conflicts_with_window';
  else if (hasFrom && !hasTo) fields.scheduled_to = 'required';
  else if (hasTo && !hasFrom) fields.scheduled_from = 'required';
  else if (hasFrom && hasTo && Date.parse(patch.scheduled_from!) >= Date.parse(patch.scheduled_to!)) {
    fields.scheduled_to = 'not_after_scheduled_from';
  }
  if (Object.keys(fields).length) throw Errors.validation(fields);
  return patch;
}

/**
 * Buyurtmalar (ma'lumot trip-order servisida). Admin tahrirlashi va bekor qilishi — trip-order ichki API orqali;
 * audit trip-order muvaffaqiyatli javobidan keyin (oldingi/yangi qiymat bilan) yoziladi.
 */
@Controller('admin/v1/orders')
export class OrdersController {
  constructor(
    private readonly orders: OrdersPort,
    private readonly ds: DataSource,
    private readonly audit: AuditService,
    @Inject(APP_CONFIG) private readonly cfg: AppConfig,
  ) {}

  @Get() @Perm('orders:read')
  list(@Query() q: OrderListQuery): Promise<Page<OrderView>> {
    const { from, to } = parseRange(q.from_date, q.to_date, this.cfg.tzOffsetHours);
    return this.orders.list({
      status: q.status, from, to, fromDate: q.from_date, toDate: q.to_date, passengerId: q.passenger_id?.toLowerCase(), q: q.q,
    }, q);
  }

  @Get(':id') @Perm('orders:read')
  async get(@Param('id', new UuidParamPipe()) id: string): Promise<OrderView> {
    const o = await this.orders.get(id);
    if (!o) throw Errors.orderNotFound(id);
    return o;
  }

  @Patch(':id') @Perm('orders:write')
  async update(
    @CurrentActor() actor: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: AdminOrderUpdateDto,
  ): Promise<OrderView> {
    const patch = checkPatch(dto);
    const before = await this.orders.get(id);
    if (!before) throw Errors.orderNotFound(id);
    const after = await this.orders.update(id, patch);
    if (!after) throw Errors.orderNotFound(id);
    await this.writeAudit(actor, 'order.update', id, snap(before), snap(after));
    return after;
  }

  @Post(':id/cancel') @Perm('orders:write') @HttpCode(200)
  async cancel(
    @CurrentActor() actor: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: AdminOrderCancelDto,
  ): Promise<OrderView> {
    const before = await this.orders.get(id);
    if (!before) throw Errors.orderNotFound(id);
    const body = { reason: dto.reason, ...(dto.comment !== undefined ? { comment: dto.comment } : {}) };
    const after = await this.orders.cancel(id, body);
    if (!after) throw Errors.orderNotFound(id);
    await this.writeAudit(actor, 'order.cancel', id, snap(before), {
      ...snap(after), cancel_reason: dto.reason, cancel_comment: dto.comment ?? null,
    });
    return after;
  }

  private writeAudit(actor: Actor, action: string, id: string, before: Record<string, unknown>, after: Record<string, unknown>) {
    return this.ds.transaction((em) => this.audit.record(em, actor, { action, entityType: 'order', entityId: id, before, after }));
  }
}
