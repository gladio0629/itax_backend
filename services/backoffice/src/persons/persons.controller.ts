import { Body, Controller, Delete, Get, HttpCode, Param, Post, Put, Query, Type } from '@nestjs/common';
import { Actor } from '../audit/audit.service';
import { CurrentActor, Perm } from '../auth/admin.guard';
import { UuidParamPipe } from '../common/pipes';
import { BlockDto, PersonCreateDto, PersonListQuery, PersonUpdateDto } from './persons.dto';
import { PersonKind, PersonsService } from './persons.service';

/** /admin/v1/drivers va /admin/v1/passengers — bir xil shakl, farqi faqat ruxsat bo'limi va ma'lumot turi. */
function makeController(kind: PersonKind, path: string, section: string): Type<unknown> {
  const read = `${section}:read`;
  const write = `${section}:write`;

  @Controller(`admin/v1/${path}`)
  class PersonsController {
    constructor(readonly svc: PersonsService) {}

    @Get() @Perm(read)
    list(@Query() q: PersonListQuery) { return this.svc.list(kind, q); }

    @Post() @Perm(write)
    create(@CurrentActor() a: Actor, @Body() dto: PersonCreateDto) { return this.svc.create(a, kind, dto); }

    @Get(':id') @Perm(read)
    get(@Param('id', new UuidParamPipe()) id: string) { return this.svc.get(kind, id); }

    @Put(':id') @Perm(write)
    update(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: PersonUpdateDto) {
      return this.svc.update(a, kind, id, dto);
    }

    @Delete(':id') @Perm(write) @HttpCode(204)
    async remove(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string) { await this.svc.archive(a, kind, id); }

    @Put(':id/block') @Perm(write)
    block(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: BlockDto) {
      return this.svc.block(a, kind, id, dto.state_reason);
    }

    @Put(':id/unblock') @Perm(write)
    unblock(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string) { return this.svc.unblock(a, kind, id); }

    @Put(':id/archive') @Perm(write)
    archive(@CurrentActor() a: Actor, @Param('id', new UuidParamPipe()) id: string) { return this.svc.archive(a, kind, id); }
  }
  return PersonsController;
}

export const DriversAdminController = makeController('driver', 'drivers', 'drivers');
export const PassengersAdminController = makeController('passenger', 'passengers', 'passengers');
