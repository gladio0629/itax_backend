import { Body, Controller, Delete, Get, HttpCode, Param, Post, Put, Query } from '@nestjs/common';
import { Actor } from '../audit/audit.service';
import { Authenticated, CurrentActor, Perm } from '../auth/admin.guard';
import { UuidParamPipe } from '../common/pipes';
import { AdminMeView, StaffService, StaffView } from './staff.service';
import { StaffCreateDto, StaffListQuery, StaffUpdateDto } from './staff.dto';
import type { Page } from '../common/pagination';

@Controller('admin/v1')
export class StaffController {
  constructor(private readonly staff: StaffService) {}

  @Get('me')
  @Authenticated()
  me(@CurrentActor() actor: Actor): Promise<AdminMeView> {
    return this.staff.me(actor);
  }

  @Get('staff')
  @Perm('staff:read')
  list(@CurrentActor() actor: Actor, @Query() q: StaffListQuery): Promise<Page<StaffView>> {
    return this.staff.list(actor, q);
  }

  @Post('staff')
  @Perm('staff:write')
  create(@CurrentActor() actor: Actor, @Body() dto: StaffCreateDto): Promise<StaffView> {
    return this.staff.create(actor, dto);
  }

  @Get('staff/:id')
  @Perm('staff:read')
  get(@CurrentActor() actor: Actor, @Param('id', new UuidParamPipe()) id: string): Promise<StaffView> {
    return this.staff.get(actor, id);
  }

  @Put('staff/:id')
  @Perm('staff:write')
  update(@CurrentActor() actor: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: StaffUpdateDto): Promise<StaffView> {
    return this.staff.update(actor, id, dto);
  }

  @Delete('staff/:id')
  @Perm('staff:write')
  @HttpCode(204)
  async archive(@CurrentActor() actor: Actor, @Param('id', new UuidParamPipe()) id: string): Promise<void> {
    await this.staff.archive(actor, id);
  }
}
