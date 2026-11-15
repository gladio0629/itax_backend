import { Controller, Get, Inject, Query } from '@nestjs/common';
import { Perm } from '../auth/admin.guard';
import { DateRangeQuery } from '../common/date-query';
import { parseRange } from '../common/time';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { OrdersPort, StatsPort, StatsView } from '../ports/ports';

/**
 * Statistika — marketolog ham ko'radi (roles.json: marketer faqat stats:read).
 * Buyurtma/daromad/komissiya raqamlari — OrdersPort.stats (ORDERS_MODE=http da trip-order /internal/orders/stats).
 * active_drivers, online_drivers, new_passengers, drivers_over_limit — StatsPort (HOZIR MOCK: driver/identity
 * servislaridan haqiqiy manba hali ulanmagan).
 */
@Controller('admin/v1/stats')
export class StatsController {
  constructor(
    private readonly stats: StatsPort,
    private readonly orders: OrdersPort,
    @Inject(APP_CONFIG) private readonly cfg: AppConfig,
  ) {}

  @Get() @Perm('stats:read')
  async get(@Query() q: DateRangeQuery): Promise<StatsView> {
    const range = { ...parseRange(q.from_date, q.to_date, this.cfg.tzOffsetHours), fromDate: q.from_date, toDate: q.to_date };
    const [o, p] = await Promise.all([this.orders.stats(range), this.stats.stats(range)]);
    return {
      orders_total: o.orders_total,
      orders_completed: o.orders_completed,
      orders_cancelled: o.orders_cancelled,
      active_drivers: p.active_drivers,
      online_drivers: p.online_drivers,
      new_passengers: p.new_passengers,
      revenue: o.revenue,
      commission: o.commission,
      drivers_over_limit: p.drivers_over_limit,
      orders_expired: o.orders_expired,
      average_order_value: o.average_order_value,
      orders_by_status: o.orders_by_status,
      daily: o.daily,
      top_routes: o.top_routes,
    };
  }
}
