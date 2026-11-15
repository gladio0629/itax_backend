import { Controller, Get, Param, ParseUUIDPipe, UseGuards } from '@nestjs/common';
import { InternalTokenGuard } from '../auth/internal-token.guard';
import { Errors } from '../common/errors';
import { InternalDriverResponseDto } from './dto/response.dto';
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
