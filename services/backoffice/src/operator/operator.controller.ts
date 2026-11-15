import { Body, Controller, Get, HttpCode, Param, Post, Query } from '@nestjs/common';
import { Actor } from '../audit/audit.service';
import { CurrentActor, Perm } from '../auth/admin.guard';
import { UuidParamPipe } from '../common/pipes';
import { HistoryQuery, QueueCompleteDto, QueueQuery, QueueRejectDto } from './operator.dto';
import { OperatorService, QueueItemView } from './operator.service';
import type { Page } from '../common/pagination';

@Controller('admin/v1/operator')
export class OperatorController {
  constructor(private readonly op: OperatorService) {}

  @Get('queue')
  @Perm('operator:read')
  queue(@CurrentActor() actor: Actor, @Query() q: QueueQuery): Promise<Page<QueueItemView>> {
    return this.op.queue(actor, q);
  }

  @Get('history')
  @Perm('operator:read')
  history(@CurrentActor() actor: Actor, @Query() q: HistoryQuery): Promise<Page<QueueItemView>> {
    return this.op.history(actor, q);
  }

  @Post('queue/:id/start')
  @Perm('operator:write')
  @HttpCode(200)
  start(@CurrentActor() actor: Actor, @Param('id', new UuidParamPipe()) id: string): Promise<QueueItemView> {
    return this.op.start(actor, id);
  }

  @Post('queue/:id/reject')
  @Perm('operator:write')
  @HttpCode(200)
  reject(@CurrentActor() actor: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: QueueRejectDto): Promise<QueueItemView> {
    return this.op.reject(actor, id, dto);
  }

  @Post('queue/:id/complete')
  @Perm('operator:write')
  @HttpCode(200)
  complete(@CurrentActor() actor: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: QueueCompleteDto): Promise<QueueItemView> {
    return this.op.complete(actor, id, dto);
  }
}
