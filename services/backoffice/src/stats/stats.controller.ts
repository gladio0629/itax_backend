import { Controller, Get, Inject, Query } from '@nestjs/common';
import { Perm } from '../auth/admin.guard';
import { DateRangeQuery } from '../common/date-query';
import { parseRange } from '../common/time';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { StatsPort, StatsView } from '../ports/ports';

/** Statistika — marketolog ham ko'radi (roles.json: marketer faqat stats:read). */
@Controller('admin/v1/stats')
export class StatsController {
  constructor(private readonly stats: StatsPort, @Inject(APP_CONFIG) private readonly cfg: AppConfig) {}

  @Get() @Perm('stats:read')
  get(@Query() q: DateRangeQuery): Promise<StatsView> {
    return this.stats.stats(parseRange(q.from_date, q.to_date, this.cfg.tzOffsetHours));
  }
}
