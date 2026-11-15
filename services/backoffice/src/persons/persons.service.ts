import { Injectable } from '@nestjs/common';
import { DataSource } from 'typeorm';
import { Actor, AuditService } from '../audit/audit.service';
import { AppError, Errors } from '../common/errors';
import type { Page } from '../common/pagination';
import {
  AccountState, AdminDriverView, AdminPassengerView, DriversPort, PassengersPort, PersonUpdate,
} from '../ports/ports';
import { PersonCreateDto, PersonListQuery, PersonUpdateDto } from './persons.dto';

export type PersonKind = 'driver' | 'passenger';
type View = AdminDriverView | AdminPassengerView;

interface Port<T extends View> {
  list(f: { state?: AccountState; q?: string }, page: { cursor?: string; limit?: number }): Promise<Page<T>>;
  get(id: string): Promise<T | null>;
  create(i: { phone: string; first_name: string; last_name: string }): Promise<T>;
  update(id: string, patch: PersonUpdate): Promise<T | null>;
}

const notFound: Record<PersonKind, (id: string) => AppError> = {
  driver: Errors.driverNotFound,
  passenger: Errors.passengerNotFound,
};

const snap = (v: View): Record<string, unknown> => ({
  id: v.id, first_name: v.first_name, last_name: v.last_name, state: v.state, state_reason: v.state_reason,
});

/** Haydovchilar va yo'lovchilar uchun umumiy mantiq (ma'lumot boshqa servisda — port orqali). */
@Injectable()
export class PersonsService {
  constructor(
    private readonly ds: DataSource,
    private readonly audit: AuditService,
    private readonly drivers: DriversPort,
    private readonly passengers: PassengersPort,
  ) {}

  private port(kind: PersonKind): Port<View> {
    return (kind === 'driver' ? this.drivers : this.passengers) as unknown as Port<View>;
  }

  list(kind: PersonKind, q: PersonListQuery): Promise<Page<View>> {
    return this.port(kind).list({ state: q.state, q: q.q }, q);
  }

  async get(kind: PersonKind, id: string): Promise<View> {
    const v = await this.port(kind).get(id);
    if (!v) throw notFound[kind](id);
    return v;
  }

  async create(actor: Actor, kind: PersonKind, dto: PersonCreateDto): Promise<View> {
    const v = await this.port(kind).create({ phone: dto.phone, first_name: dto.first_name, last_name: dto.last_name });
    await this.writeAudit(actor, `${kind}.create`, v.id, null, snap(v));
    return v;
  }

  /**
   * Yangilash / bloklash / blokdan chiqarish / arxivlash — bitta joyda.
   * O'tishlar: active→blocked|archived, blocked→active|archived, archived→active (qaytarish).
   */
  async update(actor: Actor, kind: PersonKind, id: string, dto: PersonUpdateDto): Promise<View> {
    const patch: PersonUpdate = Object.fromEntries(Object.entries(dto).filter(([, v]) => v !== undefined));
    if (Object.keys(patch).length === 0) throw Errors.validation({ body: 'empty' });
    if (patch.state === 'blocked' && !patch.state_reason) throw Errors.validation({ state_reason: 'required' });
    if (patch.state !== 'blocked' && patch.state_reason !== undefined && patch.state === undefined) {
      throw Errors.validation({ state_reason: 'requires_state' });
    }

    const port = this.port(kind);
    const before = await port.get(id);
    if (!before) throw notFound[kind](id);

    let action = `${kind}.update`;
    if (patch.state !== undefined && patch.state !== before.state) {
      const ok: Record<AccountState, AccountState[]> = {
        active: ['blocked', 'archived'], blocked: ['active', 'archived'], archived: ['active'],
      };
      if (!ok[before.state].includes(patch.state)) throw Errors.forbidden('invalid_state');
      action = patch.state === 'blocked' ? `${kind}.block`
        : patch.state === 'archived' ? `${kind}.archive`
        : before.state === 'blocked' ? `${kind}.unblock` : `${kind}.restore`;
    } else if (patch.state !== undefined) {
      delete patch.state; // allaqachon shu holatda — holat o'zgarmaydi (idempotent)
      delete patch.state_reason;
      if (Object.keys(patch).length === 0) return before;
    }

    const after = await port.update(id, patch);
    if (!after) throw notFound[kind](id);
    await this.writeAudit(actor, action, id, snap(before), snap(after));
    return after;
  }

  block(actor: Actor, kind: PersonKind, id: string, reason: string): Promise<View> {
    return this.update(actor, kind, id, { state: 'blocked', state_reason: reason });
  }
  unblock(actor: Actor, kind: PersonKind, id: string): Promise<View> {
    return this.requireState(actor, kind, id, 'blocked', 'active');
  }
  archive(actor: Actor, kind: PersonKind, id: string): Promise<View> {
    return this.update(actor, kind, id, { state: 'archived' });
  }

  /** unblock faqat bloklangan akkauntga (arxivdan qaytarish — PUT state=active) */
  private async requireState(actor: Actor, kind: PersonKind, id: string, need: AccountState, to: AccountState): Promise<View> {
    const cur = await this.get(kind, id);
    if (cur.state !== need) throw Errors.forbidden('invalid_state');
    return this.update(actor, kind, id, { state: to });
  }

  private writeAudit(actor: Actor, action: string, id: string, before: Record<string, unknown> | null, after: Record<string, unknown>) {
    return this.ds.transaction((em) => this.audit.record(em, actor, {
      action, entityType: action.split('.')[0], entityId: id, before, after,
    }));
  }
}
