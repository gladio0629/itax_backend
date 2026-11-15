import { Controller, Get, Inject, Param, Query } from '@nestjs/common';
import { Perm } from '../auth/admin.guard';
import { Errors } from '../common/errors';
import type { Page } from '../common/pagination';
import { UuidParamPipe } from '../common/pipes';
import { parseRange } from '../common/time';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { OrdersPort, OrderView } from '../ports/ports';
import { OrderListQuery } from './orders.dto';

/** Buyurtmalar — faqat ko'rish (ma'lumot trip-order servisida). */
@Controller('admin/v1/orders')
export class OrdersController {
  constructor(private readonly orders: OrdersPort, @Inject(APP_CONFIG) private readonly cfg: AppConfig) {}

  @Get() @Perm('orders:read')
  list(@Query() q: OrderListQuery): Promise<Page<OrderView>> {
    const { from, to } = parseRange(q.from_date, q.to_date, this.cfg.tzOffsetHours);
    return this.orders.list({ status: q.status, from, to, q: q.q }, q);
  }

  @Get(':id') @Perm('orders:read')
  async get(@Param('id', new UuidParamPipe()) id: string): Promise<OrderView> {
    const o = await this.orders.get(id);
    if (!o) throw Errors.orderNotFound(id);
    return o;
  }
}
