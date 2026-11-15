import { createServer, IncomingMessage, Server, ServerResponse } from 'http';
import { AddressInfo } from 'net';
import { Errors } from '../src/common/errors';
import { loadConfig } from '../src/config/configuration';
import { HttpOrdersPort } from '../src/ports/http-orders';
import { InternalHttpClient } from '../src/ports/internal-http';
import { createHarness, Harness } from './support/harness';

const TOKEN = 'test-internal-token-0123456789';
const O1 = '00000000-0000-4000-8000-000000000401'; // completed
const O4 = '00000000-0000-4000-8000-000000000404'; // in_progress
const O5 = '00000000-0000-4000-8000-000000000405'; // searching
const P1 = '00000000-0000-4000-8000-000000000301';
const MISSING = '00000000-0000-4000-8000-00000000dead';

describe('buyurtmalar (mock rejim): admin tahrirlash va bekor qilish', () => {
  let h: Harness;
  beforeEach(async () => { h = await createHarness(); });
  afterEach(async () => { await h.app.close(); });

  it('ro\'yxat: passenger_id filtri; q qabul qilinadi; trip-order maydonlari', async () => {
    const r = await h.as('root').get(`/admin/v1/orders?passenger_id=${P1}`);
    expect(r.status).toBe(200);
    expect(r.body.data.map((o: any) => o.id).sort()).toEqual([O1, O4]);
    expect(r.body.data[0]).toMatchObject({ passenger_id: P1, from_city_id: expect.any(String), to_city_id: expect.any(String) });
    expect((await h.as('root').get('/admin/v1/orders?passenger_id=abc')).status).toBe(422);
    expect((await h.as('root').get('/admin/v1/orders?q=Yunusobod 5')).body.data.map((o: any) => o.id)).toEqual([O5]);
  });

  it('PATCH: tahrirlash → yangi Order; audit oldingi/yangi qiymat bilan', async () => {
    const r = await h.as('manager').patch(`/admin/v1/orders/${O5}`).send({ comment: '  Kirish 3-podyezd ', payment_method: 'card' });
    expect(r.status).toBe(200);
    expect(r.body).toMatchObject({ id: O5, comment: 'Kirish 3-podyezd', payment_method: 'card', status: 'searching' });
    const w = await h.as('manager').patch(`/admin/v1/orders/${O5}`)
      .send({ scheduled_from: '2026-11-20T08:00:00+05:00', scheduled_to: '2026-11-20T09:00:00+05:00' });
    expect(w.body).toMatchObject({ scheduled_from: '2026-11-20T08:00:00+05:00', scheduled_to: '2026-11-20T09:00:00+05:00', scheduled_at: null });
    const a = await h.audit('order.update');
    expect(a).toHaveLength(2);
    expect(a[0]).toMatchObject({
      actor_id: h.people.manager.id, entity_id: O5,
      before: { comment: null, payment_method: 'cash', status: 'searching' }, after: { comment: 'Kirish 3-podyezd', payment_method: 'card' },
    });
  });

  it('PATCH validatsiyasi: bo\'sh tana, enum, uzunlik, RFC3339, scheduled_at va oraliq, from < to, null, ortiqcha maydon', async () => {
    const p = (b: object) => h.as('manager').patch(`/admin/v1/orders/${O5}`).send(b);
    const fields = async (b: object) => { const r = await p(b); expect(r.status).toBe(422); return r.body.error.params.fields; };
    expect(await fields({})).toEqual({ body: 'empty' });
    expect(await fields({ payment_method: 'bitcoin' })).toHaveProperty('payment_method');
    expect(await fields({ comment: 'x'.repeat(201) })).toHaveProperty('comment');
    expect(await fields({ comment: null })).toHaveProperty('comment');
    expect(await fields({ scheduled_at: '2026-11-20' })).toHaveProperty('scheduled_at');
    expect(await fields({ scheduled_at: '2026-11-20T10:00:00Z', scheduled_from: '2026-11-20T09:00:00Z', scheduled_to: '2026-11-20T11:00:00Z' }))
      .toEqual({ scheduled_at: 'conflicts_with_window' });
    expect(await fields({ scheduled_from: '2026-11-20T09:00:00Z' })).toEqual({ scheduled_to: 'required' });
    expect(await fields({ scheduled_to: '2026-11-20T09:00:00Z' })).toEqual({ scheduled_from: 'required' });
    expect(await fields({ scheduled_from: '2026-11-20T10:00:00Z', scheduled_to: '2026-11-20T15:00:00+05:00' }))
      .toEqual({ scheduled_to: 'not_after_scheduled_from' });
    expect(await fields({ status: 'cancelled' })).toHaveProperty('status');
    expect((await p({ comment: 'x'.repeat(200) })).status).toBe(200);
    expect((await h.as('manager').patch(`/admin/v1/orders/abc`).send({ comment: 'x' })).status).toBe(422);
  });

  it('PATCH: 404 ORDER_NOT_FOUND; boshlangan / yakunlangan — 403 (trip_started / order_closed); audit yo\'q', async () => {
    const nf = await h.as('manager').patch(`/admin/v1/orders/${MISSING}`).send({ comment: 'x' });
    expect(nf.status).toBe(404);
    expect(nf.body).toEqual({ error: { key: 'ORDER_NOT_FOUND', params: { order_id: MISSING } } });
    const started = await h.as('manager').patch(`/admin/v1/orders/${O4}`).send({ comment: 'x' });
    expect(started.status).toBe(403);
    expect(started.body).toEqual({ error: { key: 'FORBIDDEN', params: { reason: 'trip_started' } } });
    expect((await h.as('manager').patch(`/admin/v1/orders/${O1}`).send({ comment: 'x' })).body.error.params.reason).toBe('order_closed');
    expect(await h.audit()).toHaveLength(0);
  });

  it('bekor qilish: sabab enum, izoh ≤500; cancelled + cancel_reason; qayta — 403 order_closed; audit', async () => {
    const url = `/admin/v1/orders/${O5}/cancel`;
    expect((await h.as('manager').post(url).send({})).body.error.params.fields).toHaveProperty('reason');
    expect((await h.as('manager').post(url).send({ reason: 'bored' })).status).toBe(422);
    expect((await h.as('manager').post(url).send({ reason: 'fraud', comment: 'x'.repeat(501) })).status).toBe(422);
    const r = await h.as('manager').post(url).send({ reason: 'duplicate', comment: 'Ikki marta yaratilgan' });
    expect(r.status).toBe(200);
    expect(r.body).toMatchObject({ id: O5, status: 'cancelled', cancel_reason: 'duplicate' });
    const again = await h.as('manager').post(url).send({ reason: 'duplicate' });
    expect(again.status).toBe(403);
    expect(again.body.error.params.reason).toBe('order_closed');
    expect((await h.as('manager').post(`/admin/v1/orders/${O4}/cancel`).send({ reason: 'fraud' })).body.error.params.reason).toBe('trip_started');
    expect((await h.as('manager').post(`/admin/v1/orders/${MISSING}/cancel`).send({ reason: 'fraud' })).status).toBe(404);
    const a = await h.audit('order.cancel');
    expect(a).toHaveLength(1);
    expect(a[0]).toMatchObject({
      entity_id: O5, before: { status: 'searching' },
      after: { status: 'cancelled', cancel_reason: 'duplicate', cancel_comment: 'Ikki marta yaratilgan' },
    });
  });

  it('ruxsat: orders:write (marketolog va operator — 403)', async () => {
    for (const who of ['marketer', 'op1']) {
      expect((await h.as(who).patch(`/admin/v1/orders/${O5}`).send({ comment: 'x' })).status).toBe(403);
      expect((await h.as(who).post(`/admin/v1/orders/${O5}/cancel`).send({ reason: 'fraud' })).status).toBe(403);
    }
    expect((await h.as('owner').patch(`/admin/v1/orders/${O5}`).send({ comment: 'x' })).status).toBe(200);
  });

  it('statistika: yangi maydonlar (orders_expired, average_order_value, orders_by_status, daily, top_routes)', async () => {
    const s = await h.as('marketer').get('/admin/v1/stats');
    expect(s.status).toBe(200);
    expect(s.body).toEqual({
      orders_total: 5, orders_completed: 2, orders_cancelled: 1, active_drivers: 2, online_drivers: 1, new_passengers: 3,
      revenue: 4_300_000, commission: 645_000, drivers_over_limit: 1,
      orders_expired: 0, average_order_value: 2_150_000,
      orders_by_status: { completed: 2, cancelled: 1, in_progress: 1, searching: 1 },
      daily: [
        { date: '2026-11-01', orders: 1, completed: 1, revenue: 2_500_000 },
        { date: '2026-11-05', orders: 1, completed: 1, revenue: 1_800_000 },
        { date: '2026-11-08', orders: 1, completed: 0, revenue: 0 },
        { date: '2026-11-11', orders: 1, completed: 0, revenue: 0 },
        { date: '2026-11-12', orders: 1, completed: 0, revenue: 0 },
      ],
      top_routes: [
        { from_city_id: expect.any(String), to_city_id: expect.any(String), from_name: 'Toshkent', to_name: 'Toshkent', orders: 4 },
        { from_city_id: expect.any(String), to_city_id: expect.any(String), from_name: 'Toshkent', to_name: 'Samarqand', orders: 1 },
      ],
    });
  });
});

/** Trip & Order ichki API sini taqlaydigan HTTP server */
class TripOrderStub {
  server!: Server;
  url = '';
  calls: { method: string; url: string; token?: string; body: any }[] = [];
  mode: 'ok' | 'error500' | 'hang' = 'ok';
  orders = new Map<string, any>();
  locked = new Map<string, string>(); // id -> reason (403)

  async start(): Promise<void> {
    this.server = createServer((req, res) => void this.handle(req, res));
    await new Promise<void>((r) => this.server.listen(0, '127.0.0.1', r));
    this.url = `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
  }
  stop(): Promise<void> {
    this.server.closeAllConnections?.();
    return new Promise((r) => this.server.close(() => r()));
  }

  private async handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const chunks: Buffer[] = [];
    for await (const c of req) chunks.push(c as Buffer);
    const raw = Buffer.concat(chunks).toString('utf8');
    const token = req.headers['x-internal-token'] as string | undefined;
    this.calls.push({ method: req.method!, url: req.url!, token, body: raw ? JSON.parse(raw) : null });
    const send = (code: number, body: unknown) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(body)); };
    if (this.mode === 'hang') return;
    if (this.mode === 'error500') return send(500, { error: { key: 'INTERNAL_ERROR', params: {} } });
    if (token !== TOKEN) return send(403, { error: { key: 'FORBIDDEN', params: { reason: 'internal_only' } } });

    const u = new URL(req.url!, 'http://x');
    if (req.method === 'GET' && u.pathname === '/internal/orders') {
      return send(200, { data: [...this.orders.values()], cursor: 'opaque-next' });
    }
    if (req.method === 'GET' && u.pathname === '/internal/orders/stats') {
      return send(200, {
        orders_total: 10, orders_completed: 6, orders_cancelled: 3, orders_expired: 1,
        orders_by_status: { completed: 6, cancelled: 3, expired: 1 }, revenue: 600, commission: 90, average_order_value: 100,
        daily: [{ date: '2026-11-10', orders: 10, completed: 6, revenue: 600 }],
        top_routes: [{ from_city_id: 'c1', to_city_id: 'c2', from_name: 'Toshkent', to_name: 'Buxoro', orders: 7 }],
      });
    }
    const m = /^\/internal\/orders\/([^/]+)(\/cancel)?$/.exec(u.pathname);
    if (m) {
      const o = this.orders.get(m[1]);
      if (!o) return send(404, { error: { key: 'ORDER_NOT_FOUND', params: {} } });
      if (req.method === 'GET' && !m[2]) return send(200, o);
      const lock = this.locked.get(m[1]);
      if (lock) return send(403, { error: { key: 'FORBIDDEN', params: { reason: lock } } });
      const b = JSON.parse(raw || '{}');
      if (req.method === 'PATCH' && !m[2]) {
        if (b.comment === 'bad') return send(422, { error: { key: 'VALIDATION_ERROR', params: { fields: { comment: 'invalid' } } } });
        const next = { ...o, ...b };
        this.orders.set(o.id, next);
        return send(200, next);
      }
      if (req.method === 'POST' && m[2]) {
        const next = { ...o, status: 'cancelled', cancel_reason: b.reason };
        this.orders.set(o.id, next);
        return send(200, next);
      }
    }
    send(404, { error: { key: 'NOT_FOUND', params: {} } });
  }
}

const upstreamOrder = (id: string, extra: object = {}) => ({
  id, type: 'passenger', seats: 1, seat_ids: ['s1'], car_class: 'comfort', vehicle_type: 'sedan',
  from: { lat: 41.3, lng: 69.2, address: 'A' }, to: { lat: 39.6, lng: 66.9, address: 'B' },
  scheduled_at: null, scheduled_from: null, scheduled_to: null, payment_method: 'cash', comment: null, status: 'searching',
  price: 150_000_00, distance_m: 300_000, duration_s: 14_400, driver: null, trip_id: null, created_at: '2026-11-10T10:00:00Z',
  passenger_id: P1, cancel_reason: null, from_city_id: 'c1', to_city_id: 'c2', future_field: { x: 1 }, ...extra,
});

describe('Trip & Order HTTP porti', () => {
  const stub = new TripOrderStub();
  beforeAll(() => stub.start());
  afterAll(() => stub.stop());
  beforeEach(() => {
    stub.calls = []; stub.mode = 'ok'; stub.orders.clear(); stub.locked.clear();
    stub.orders.set(O5, upstreamOrder(O5));
    stub.orders.set(O4, upstreamOrder(O4, { status: 'in_progress' }));
  });
  const port = (timeoutMs = 500, token = TOKEN, url = stub.url) =>
    new HttpOrdersPort(new InternalHttpClient(url, token, timeoutMs, fetch, Errors.tripOrderUnavailable));

  it('list: filtrlar so\'rov satrida (q yuborilmaydi), javob o\'zgarishsiz; cursor shaffof', async () => {
    const r = await port().list(
      { status: 'searching', fromDate: '2026-11-01', toDate: '2026-11-10', passengerId: P1, q: 'nimadir', from: new Date(0) },
      { cursor: 'abc', limit: 5 },
    );
    expect(r.cursor).toBe('opaque-next');
    expect(r.data[0]).toEqual(upstreamOrder(O5));
    const u = new URL(stub.calls[0].url, 'http://x');
    expect(u.pathname).toBe('/internal/orders');
    expect(Object.fromEntries(u.searchParams)).toEqual({
      status: 'searching', from_date: '2026-11-01', to_date: '2026-11-10', passenger_id: P1, cursor: 'abc', limit: '5',
    });
    expect(stub.calls[0].token).toBe(TOKEN);
  });

  it('get: 200 → Order, 404 → null; 5xx / timeout / ulanib bo\'lmasa / token xato → 502 TRIP_ORDER_UNAVAILABLE', async () => {
    expect(await port().get(O5)).toEqual(upstreamOrder(O5));
    expect(await port().get(MISSING)).toBeNull();
    stub.mode = 'error500';
    await expect(port().get(O5)).rejects.toMatchObject({ status: 502, key: 'TRIP_ORDER_UNAVAILABLE', params: { reason: 'http_500' } });
    stub.mode = 'hang';
    await expect(port(150).get(O5)).rejects.toMatchObject({ status: 502, key: 'TRIP_ORDER_UNAVAILABLE', params: { reason: 'timeout' } });
    stub.mode = 'ok';
    await expect(port(500, TOKEN, 'http://127.0.0.1:1').get(O5)).rejects.toMatchObject({ status: 502, params: { reason: 'unreachable' } });
    await expect(port(500, 'boshqa-token-0123456789').cancel(O5, { reason: 'fraud' })).rejects.toMatchObject({ status: 502, params: { reason: 'http_403' } });
  });

  it('update/cancel: PATCH / POST tanasi; 403 → FORBIDDEN (o\'sha reason); 422 → VALIDATION_ERROR (o\'sha params); 404 → null', async () => {
    const u = await port().update(O5, { comment: 'salom', payment_method: 'card' });
    expect(u).toMatchObject({ id: O5, comment: 'salom', payment_method: 'card', future_field: { x: 1 } });
    expect(stub.calls[0]).toMatchObject({ method: 'PATCH', url: `/internal/orders/${O5}`, body: { comment: 'salom', payment_method: 'card' } });
    await expect(port().update(O5, { comment: 'bad' })).rejects.toMatchObject({
      status: 422, key: 'VALIDATION_ERROR', params: { fields: { comment: 'invalid' } },
    });
    expect(await port().update(MISSING, { comment: 'x' })).toBeNull();

    stub.locked.set(O4, 'trip_started');
    await expect(port().cancel(O4, { reason: 'fraud' })).rejects.toMatchObject({ status: 403, key: 'FORBIDDEN', params: { reason: 'trip_started' } });
    stub.calls = [];
    const c = await port().cancel(O5, { reason: 'admin_decision', comment: 'izoh' });
    expect(c).toMatchObject({ status: 'cancelled', cancel_reason: 'admin_decision' });
    expect(stub.calls[0]).toMatchObject({ method: 'POST', url: `/internal/orders/${O5}/cancel`, body: { reason: 'admin_decision', comment: 'izoh' } });
    expect(await port().cancel(MISSING, { reason: 'fraud' })).toBeNull();
  });

  describe('backoffice ORDERS_MODE=http orqali', () => {
    let h: Harness;
    beforeEach(async () => {
      h = await createHarness({
        identity: { mode: 'mock', baseUrl: '', internalToken: TOKEN, timeoutMs: 300 },
        orders: { mode: 'http', baseUrl: stub.url },
      });
    });
    afterEach(async () => { await h.app.close(); });

    it('ro\'yxat va batafsil — trip-order javobi o\'zgarishsiz; 404 → ORDER_NOT_FOUND', async () => {
      const r = await h.as('root').get(`/admin/v1/orders?status=searching&from_date=2026-11-01&passenger_id=${P1}&q=abc`);
      expect(r.status).toBe(200);
      expect(r.body).toEqual({ data: [upstreamOrder(O5), upstreamOrder(O4, { status: 'in_progress' })], cursor: 'opaque-next' });
      expect(Object.fromEntries(new URL(stub.calls[0].url, 'http://x').searchParams))
        .toEqual({ status: 'searching', from_date: '2026-11-01', passenger_id: P1 });
      expect((await h.as('root').get(`/admin/v1/orders/${O5}`)).body).toEqual(upstreamOrder(O5));
      const nf = await h.as('root').get(`/admin/v1/orders/${MISSING}`);
      expect(nf.status).toBe(404);
      expect(nf.body).toEqual({ error: { key: 'ORDER_NOT_FOUND', params: { order_id: MISSING } } });
    });

    it('PATCH va cancel; 403 trip_started; 502; audit faqat muvaffaqiyatda', async () => {
      const u = await h.as('manager').patch(`/admin/v1/orders/${O5}`).send({ comment: 'yangi izoh' });
      expect(u.status).toBe(200);
      expect(u.body).toMatchObject({ id: O5, comment: 'yangi izoh' });
      stub.locked.set(O4, 'trip_started');
      const f = await h.as('manager').post(`/admin/v1/orders/${O4}/cancel`).send({ reason: 'fraud' });
      expect(f.status).toBe(403);
      expect(f.body).toEqual({ error: { key: 'FORBIDDEN', params: { reason: 'trip_started' } } });
      const c = await h.as('manager').post(`/admin/v1/orders/${O5}/cancel`).send({ reason: 'passenger_request' });
      expect(c.status).toBe(200);
      expect(c.body).toMatchObject({ status: 'cancelled', cancel_reason: 'passenger_request' });
      stub.mode = 'error500';
      const down = await h.as('manager').patch(`/admin/v1/orders/${O5}`).send({ comment: 'x' });
      expect(down.status).toBe(502);
      expect(down.body).toEqual({ error: { key: 'TRIP_ORDER_UNAVAILABLE', params: { reason: 'http_500' } } });
      expect((await h.audit()).map((a) => a.action)).toEqual(['order.update', 'order.cancel']);
    });

    it('statistika: buyurtma raqamlari trip-order dan, haydovchi/yo\'lovchi raqamlari StatsPort dan', async () => {
      const s = await h.as('marketer').get('/admin/v1/stats?from_date=2026-11-01&to_date=2026-11-30');
      expect(s.status).toBe(200);
      expect(s.body).toEqual({
        orders_total: 10, orders_completed: 6, orders_cancelled: 3, active_drivers: 2, online_drivers: 1, new_passengers: 1, // mock: 10-noyabr
        revenue: 600, commission: 90, drivers_over_limit: 1, orders_expired: 1, average_order_value: 100,
        orders_by_status: { completed: 6, cancelled: 3, expired: 1 },
        daily: [{ date: '2026-11-10', orders: 10, completed: 6, revenue: 600 }],
        top_routes: [{ from_city_id: 'c1', to_city_id: 'c2', from_name: 'Toshkent', to_name: 'Buxoro', orders: 7 }],
      });
      expect(stub.calls.at(-1)!.url).toBe('/internal/orders/stats?from_date=2026-11-01&to_date=2026-11-30');
      stub.mode = 'error500';
      expect((await h.as('marketer').get('/admin/v1/stats')).body.error.key).toBe('TRIP_ORDER_UNAVAILABLE');
    });
  });
});

describe('konfiguratsiya: ORDERS_MODE, TRIP_ORDER_INTERNAL_URL, REDIS_URL', () => {
  const base = { DB_HOST: 'h', DB_USER: 'u', DB_PASSWORD: 'p', DB_NAME: 'd', JWT_PUBLIC_KEY_PEM: 'x', IDENTITY_STAFF_MODE: 'mock' };

  it('defaultlar: orders mock, trip-order:8012, redis://redis:6379', () => {
    const cfg = loadConfig(base as never);
    expect(cfg.orders).toEqual({ mode: 'mock', baseUrl: 'http://trip-order:8012' });
    expect(cfg.redis).toEqual({ url: 'redis://redis:6379', timeoutMs: 1000 });
  });

  it('ORDERS_MODE=http — INTERNAL_TOKENS shart; noto\'g\'ri rejim — xato; URL lar o\'qiladi', () => {
    expect(() => loadConfig({ ...base, ORDERS_MODE: 'http' } as never)).toThrow(/INTERNAL_TOKENS/);
    expect(() => loadConfig({ ...base, ORDERS_MODE: 'grpc' } as never)).toThrow(/ORDERS_MODE/);
    const cfg = loadConfig({
      ...base, ORDERS_MODE: 'http', INTERNAL_TOKENS: TOKEN, TRIP_ORDER_INTERNAL_URL: 'http://to:9/', REDIS_URL: 'redis://r:1', REDIS_TIMEOUT_MS: '250',
    } as never);
    expect(cfg.orders).toEqual({ mode: 'http', baseUrl: 'http://to:9' });
    expect(cfg.identity.internalToken).toBe(TOKEN);
    expect(cfg.redis).toEqual({ url: 'redis://r:1', timeoutMs: 250 });
  });
});
