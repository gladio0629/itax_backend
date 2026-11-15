import { Body, Controller, Get, HttpCode, Param, Post, Query } from '@nestjs/common';
import { Actor } from '../audit/audit.service';
import { CurrentActor, Perm } from '../auth/admin.guard';
import type { Page } from '../common/pagination';
import { UuidParamPipe } from '../common/pipes';
import {
  ComplaintAssignDto, ComplaintCreateDto, ComplaintListQuery, ComplaintNoteDto, ComplaintStatusDto,
} from './complaints.dto';
import { ComplaintDetailView, ComplaintNoteView, ComplaintsService, ComplaintView } from './complaints.service';

@Controller('admin/v1/complaints')
export class ComplaintsController {
  constructor(private readonly svc: ComplaintsService) {}

  @Get() @Perm('complaints:read')
  list(@Query() q: ComplaintListQuery): Promise<Page<ComplaintView>> { return this.svc.list(q); }

  @Get(':id') @Perm('complaints:read')
  get(@Param('id', new UuidParamPipe()) id: string): Promise<ComplaintDetailView> { return this.svc.get(id); }

  @Post() @Perm('complaints:write')
  create(@CurrentActor() a: Actor, @Body() dto: ComplaintCreateDto): Promise<ComplaintView> { return this.svc.create(a, dto); }

  @Post(':id/assign') @Perm('complaints:write') @HttpCode(200)
  assign(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: ComplaintAssignDto): Promise<ComplaintView> {
    return this.svc.assign(a, id, dto);
  }

  @Post(':id/status') @Perm('complaints:write') @HttpCode(200)
  status(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: ComplaintStatusDto): Promise<ComplaintView> {
    return this.svc.setStatus(a, id, dto);
  }

  @Post(':id/notes') @Perm('complaints:write')
  note(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: ComplaintNoteDto): Promise<ComplaintNoteView> {
    return this.svc.addNote(a, id, dto);
  }
}
