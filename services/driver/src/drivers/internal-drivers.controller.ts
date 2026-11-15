import { Controller, Get, Param, ParseUUIDPipe, Query, UseGuards } from '@nestjs/common';
import { InternalTokenGuard } from '../auth/internal-token.guard';
import { Errors } from '../common/errors';
import { DriverStatsQueryDto } from './dto/request.dto';
import { DriverStatsResponseDto, InternalDriverResponseDto } from './dto/response.dto';
import { DriversService } from './drivers.service';

/** Ichki API: Kong tashqariga chiqarmaydi, qo'shimcha ravishda X-Internal-Token talab qilinadi. */
@Controller('internal/driver/drivers')
@UseGuards(InternalTokenGuard)
export class InternalDriversController {
  constructor(private readonly drivers: DriversService) {}

  @Get(':id')
  getById(
    @Param('id', new ParseUUIDPipe({ exceptionFactory: () => Errors.validation({ id: 'invalid_uuid' }) })) id: string,
  ): Promise<InternalDriverResponseDto> {
    return this.drivers.getInternal(id);
  }
}

/** Ichki statistika (Backoffice admin dashboard). Kong orqali chiqmaydi; X-Internal-Token talab qilinadi. */
@Controller('internal/driver')
@UseGuards(InternalTokenGuard)
export class InternalDriverStatsController {
  constructor(private readonly drivers: DriversService) {}

  /** ?from_date=YYYY-MM-DD&to_date=YYYY-MM-DD (UTC, ikkalasi ixtiyoriy; to_date kuni ham kiradi) */
  @Get('stats')
  stats(@Query() q: DriverStatsQueryDto): Promise<DriverStatsResponseDto> {
    return this.drivers.stats(q.from_date, q.to_date);
  }
}
