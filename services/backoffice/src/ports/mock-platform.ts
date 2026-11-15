import { Injectable } from '@nestjs/common';
import { v4 as uuidv4 } from 'uuid';
import { Errors } from '../common/errors';
import { Page, PageQuery, pageArray } from '../common/pagination';
import {
  AccountState, AdminDriverView, AdminPassengerView, DriverLookupPort, DriversPort, IdentityStaff, IdentityStaffPort, UserLookupPort,
  LedgerEntryView, OrdersPort, OrderView, PassengersPort, PersonCreate, PersonUpdate, StatsPort,
  StatsView, WalletPort,
} from './ports';

const uid = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const iso = (d: string) => new Date(d).toISOString();
const byDate = <T extends { created_at: string }>(r: T) => new Date(r.created_at);

/**
 * Mock ma'lumotlar: driver / passenger / trip-order / wallet servislari o'rniga xotirada.
 * Ular tayyor bo'lganda har bir port HTTP/NATS mijozi bilan almashtiriladi.
 */
@Injectable()
export class MockData {
  readonly drivers = new Map<string, AdminDriverView>();
  readonly passengers = new Map<string, AdminPassengerView>();
  readonly orders: OrderView[] = [];
  readonly ledger: LedgerEntryView[] = [];
  readonly wallets = new Map<string, number>();
  readonly driverWallet = new Map<string, string>();
  readonly identityStaff = new Map<string, IdentityStaff>();

  constructor() {
    const car = (model: string, plate: string) => ({
      brand: 'Chevrolet', model, year: 2020, color: 'Oq', plate_number: plate, car_class: 'economy', seats: 4,
    });
    const drv = (n: number, first: string, phone: string, st: AccountState, model: string, plate: string, bal: number, at: string) => {
      this.drivers.set(uid(100 + n), {
        id: uid(100 + n), phone, first_name: first, last_name: 'Karimov', state: st,
        state_reason: st === 'blocked' ? 'documents_expired' : null, driver_state: st === 'active' ? 'active' : 'blocked',
        car: car(model, plate), rating: 4.8, trips_count: 40 * n, wallet_balance: bal, created_at: iso(at),
      });
      this.wallets.set(uid(200 + n), bal);
      this.driverWallet.set(uid(100 + n), uid(200 + n));
    };
    drv(1, 'Ali', '+998901110001', 'active', 'Cobalt', '01A123BC', 150_000, '2026-10-01');
    drv(2, 'Vali', '+998901110002', 'active', 'Gentra', '01B456CD', -450_000, '2026-10-05');
    drv(3, 'Sher', '+998901110003', 'blocked', 'Nexia', '01C789DE', -20_000, '2026-10-10');
    ([[1, 'Dilnoza', '+998902220001', '2026-10-02'], [2, 'Bobur', '+998902220002', '2026-10-06'], [3, 'Zarina', '+998902220003', '2026-11-10']] as const)
      .forEach(([n, first, phone, at]) => this.passengers.set(uid(300 + n), {
        id: uid(300 + n), phone, first_name: first, last_name: 'Aliyeva', state: 'active', state_reason: null,
        rating: 4.9, orders_count: n * 3, created_at: iso(at),
      }));
    const order = (n: number, status: string, price: number, at: string, drvId: string | null) => this.orders.push({
      id: uid(400 + n), type: 'passenger', seats: 2,
      from: { lat: 41.31, lng: 69.24, address: `Chilonzor ${n}` }, to: { lat: 41.33, lng: 69.28, address: `Yunusobod ${n}` },
      scheduled_at: null, payment_method: 'cash', comment: null, status, price, distance_m: 5000 + n * 100, duration_s: 900,
      driver: drvId ? { id: drvId, first_name: 'Ali', rating: 4.8 } : null, trip_id: null, created_at: iso(at),
    });
    order(1, 'completed', 2_500_000, '2026-11-01T08:00:00Z', uid(101));
    order(2, 'completed', 1_800_000, '2026-11-05T09:00:00Z', uid(102));
    order(3, 'cancelled', 1_200_000, '2026-11-08T10:00:00Z', null);
    order(4, 'in_progress', 3_000_000, '2026-11-11T11:00:00Z', uid(101));
    order(5, 'searching', 2_200_000, '2026-11-12T01:00:00Z', null);
    ([[1, 'topup', 200_000, 150_000], [1, 'trip_commission', -50_000, 150_000], [2, 'topup', 100_000, -450_000], [2, 'trip_commission', -550_000, -450_000]] as const)
      .forEach(([w, kind, amount, after], i) => this.ledger.push({
        id: uid(500 + i), wallet_id: uid(200 + w), kind, amount, balance_after: after, reference_id: null,
        created_at: iso(`2026-11-0${i + 1}T10:00:00Z`),
      }));
  }
}

function matches(q: string | undefined, ...fields: (string | null | undefined)[]): boolean {
  if (!q) return true;
  const n = q.toLowerCase();
  return fields.some((f) => f?.toLowerCase().includes(n));
}

type Person = { state: AccountState; state_reason: string | null; first_name: string | null; last_name: string | null };

function applyPatch<T extends Person>(rec: T, patch: PersonUpdate): T {
  const next = { ...rec };
  if (patch.first_name !== undefined) next.first_name = patch.first_name;
  if (patch.last_name !== undefined) next.last_name = patch.last_name;
  if (patch.state !== undefined) {
    next.state = patch.state;
    next.state_reason = patch.state === 'active' ? null : (patch.state_reason ?? rec.state_reason);
  }
  return next;
}

const base = (input: PersonCreate) => ({
  id: uuidv4(), phone: input.phone, first_name: input.first_name, last_name: input.last_name,
  state: 'active' as AccountState, state_reason: null, created_at: new Date().toISOString(),
});

@Injectable()
export class MockDriversPort extends DriversPort {
  constructor(private readonly d: MockData) { super(); }
  private view(x: AdminDriverView): AdminDriverView {
    const w = this.d.driverWallet.get(x.id);
    return { ...x, wallet_balance: w ? (this.d.wallets.get(w) ?? x.wallet_balance) : x.wallet_balance };
  }
  async list(f: { state?: AccountState; q?: string }, page: PageQuery) {
    const rows = [...this.d.drivers.values()]
      .filter((x) => (!f.state || x.state === f.state) && matches(f.q, x.first_name, x.last_name, x.phone, x.car?.plate_number))
      .map((x) => this.view(x));
    return pageArray(rows, byDate, page);
  }
  async get(id: string) { const x = this.d.drivers.get(id); return x ? this.view(x) : null; }
  async create(input: PersonCreate) {
    if ([...this.d.drivers.values()].some((x) => x.phone === input.phone)) throw Errors.validation({ phone: 'taken' });
    const x: AdminDriverView = { ...base(input), driver_state: 'draft', rating: 0, trips_count: 0, wallet_balance: 0 };
    this.d.drivers.set(x.id, x);
    return x;
  }
  async update(id: string, patch: PersonUpdate) {
    const x = this.d.drivers.get(id);
    if (!x) return null;
    const n = applyPatch(x, patch);
    this.d.drivers.set(id, n);
    return this.view(n);
  }
}

@Injectable()
export class MockPassengersPort extends PassengersPort {
  constructor(private readonly d: MockData) { super(); }
  async list(f: { state?: AccountState; q?: string }, page: PageQuery) {
    const rows = [...this.d.passengers.values()]
      .filter((x) => (!f.state || x.state === f.state) && matches(f.q, x.first_name, x.last_name, x.phone));
    return pageArray(rows, byDate, page);
  }
  async get(id: string) { return this.d.passengers.get(id) ?? null; }
  async create(input: PersonCreate) {
    if ([...this.d.passengers.values()].some((x) => x.phone === input.phone)) throw Errors.validation({ phone: 'taken' });
    const x: AdminPassengerView = { ...base(input), rating: null, orders_count: 0 };
    this.d.passengers.set(x.id, x);
    return x;
  }
  async update(id: string, patch: PersonUpdate) {
    const x = this.d.passengers.get(id);
    if (!x) return null;
    const n = applyPatch(x, patch);
    this.d.passengers.set(id, n);
    return n;
  }
}

@Injectable()
export class MockOrdersPort extends OrdersPort {
  constructor(private readonly d: MockData) { super(); }
  async list(f: { status?: string; from?: Date; to?: Date; q?: string }, page: PageQuery): Promise<Page<OrderView>> {
    const rows = this.d.orders.filter((o) => {
      const t = new Date(o.created_at).getTime();
      const addr = `${(o.from as { address: string }).address} ${(o.to as { address: string }).address}`;
      return (!f.status || o.status === f.status) && (!f.from || t >= f.from.getTime()) && (!f.to || t < f.to.getTime())
        && (!f.q || o.id.startsWith(f.q.toLowerCase()) || addr.toLowerCase().includes(f.q.toLowerCase()));
    });
    return pageArray(rows, byDate, page);
  }
  async get(id: string) { return this.d.orders.find((o) => o.id === id) ?? null; }
}

@Injectable()
export class MockWalletPort extends WalletPort {
  constructor(private readonly d: MockData) { super(); }
  async ledger(f: { walletId?: string; driverId?: string }, page: PageQuery): Promise<Page<LedgerEntryView>> {
    const wid = f.walletId ?? (f.driverId ? this.d.driverWallet.get(f.driverId) ?? '-' : undefined);
    return pageArray(this.d.ledger.filter((e) => !wid || e.wallet_id === wid), byDate, page);
  }
  async walletExists(walletId: string) { return this.d.wallets.has(walletId); }
  async applyCorrection(c: { correctionId: string; walletId: string; amount: number }): Promise<void> {
    if (this.d.ledger.some((e) => e.reference_id === c.correctionId)) return; // idempotent
    const bal = (this.d.wallets.get(c.walletId) ?? 0) + c.amount;
    this.d.wallets.set(c.walletId, bal);
    this.d.ledger.push({
      id: uuidv4(), wallet_id: c.walletId, kind: 'correction', amount: c.amount, balance_after: bal,
      reference_id: c.correctionId, created_at: new Date().toISOString(),
    });
  }
}

@Injectable()
export class MockStatsPort extends StatsPort {
  constructor(private readonly d: MockData) { super(); }
  async stats(range: { from?: Date; to?: Date }): Promise<StatsView> {
    const inR = (s: string) => {
      const t = new Date(s).getTime();
      return (!range.from || t >= range.from.getTime()) && (!range.to || t < range.to.getTime());
    };
    const orders = this.d.orders.filter((o) => inR(o.created_at));
    const done = orders.filter((o) => o.status === 'completed');
    const revenue = done.reduce((s, o) => s + (o.price as number), 0);
    return {
      orders_total: orders.length,
      orders_completed: done.length,
      orders_cancelled: orders.filter((o) => o.status === 'cancelled').length,
      active_drivers: [...this.d.drivers.values()].filter((x) => x.state === 'active').length,
      online_drivers: 1,
      new_passengers: [...this.d.passengers.values()].filter((p) => inR(p.created_at)).length,
      revenue,
      commission: Math.floor((revenue * 15) / 100),
      drivers_over_limit: [...this.d.wallets.values()].filter((b) => b <= -400_000).length,
    };
  }
}

@Injectable()
export class MockIdentityStaffPort extends IdentityStaffPort {
  constructor(private readonly d: MockData) { super(); }

  async create(s: { phone: string; email: string | null; role: string; name: string }): Promise<{ id: string }> {
    // Identity bilan bir xil qoida: email/telefon band bo'lsa 409
    const all = [...this.d.identityStaff.values()];
    const em = s.email?.toLowerCase() ?? null;
    if (em && all.some((x) => x.email === em)) throw Errors.staffEmailTaken();
    if (all.some((x) => x.phone === s.phone)) throw Errors.staffPhoneTaken();
    const id = uuidv4();
    this.d.identityStaff.set(id, { id, email: s.email?.toLowerCase() ?? null, phone: s.phone, fullName: s.name, role: s.role, active: true });
    return { id };
  }

  async update(staff: IdentityStaff): Promise<void> { this.d.identityStaff.set(staff.id, { ...staff }); }
}

@Injectable()
export class MockDriverLookupPort extends DriverLookupPort {
  constructor(private readonly d: MockData) { super(); }
  async getDriver(id: string) { const x = this.d.drivers.get(id); return x ? { id, phone: x.phone } : null; }
}

@Injectable()
export class MockUserLookupPort extends UserLookupPort {
  constructor(private readonly d: MockData) { super(); }
  async getUser(id: string) { const x = this.d.passengers.get(id); return x ? { id, phone: x.phone } : null; }
}
