import { Body, Controller, Delete, Get, HttpCode, Param, Post, Put, Query } from '@nestjs/common';
import { Actor } from '../audit/audit.service';
import { CurrentActor, Perm } from '../auth/admin.guard';
import { PageQuery } from '../common/pagination';
import type { Page } from '../common/pagination';
import { UuidParamPipe } from '../common/pipes';
import { ApprovalRejectDto } from '../wallet/wallet.dto';
import { TariffInputDto } from './tariffs.dto';
import { TariffsService, TariffView } from './tariffs.service';

@Controller('admin/v1/tariffs')
export class TariffsController {
  constructor(private readonly svc: TariffsService) {}

  @Get() @Perm('tariffs:read')
  list(@Query() q: PageQuery): Promise<Page<TariffView>> { return this.svc.list(q); }

  @Post() @Perm('tariffs:write') @HttpCode(202)
  create(@CurrentActor() a: Actor, @Body() dto: TariffInputDto): Promise<TariffView> { return this.svc.create(a, dto); }

  @Get(':id') @Perm('tariffs:read')
  get(@Param('id', new UuidParamPipe()) id: string): Promise<TariffView> { return this.svc.get(id); }

  @Put(':id') @Perm('tariffs:write') @HttpCode(202)
  update(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: TariffInputDto): Promise<TariffView> {
    return this.svc.update(a, id, dto);
  }

  @Delete(':id') @Perm('tariffs:write') @HttpCode(202)
  remove(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string): Promise<TariffView> { return this.svc.remove(a, id); }

  @Post(':id/approve') @Perm('tariffs:write') @HttpCode(200)
  approve(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string): Promise<TariffView> { return this.svc.approve(a, id); }

  @Post(':id/reject') @Perm('tariffs:write') @HttpCode(200)
  reject(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: ApprovalRejectDto): Promise<TariffView> {
    return this.svc.reject(a, id, dto.reason);
  }
}
