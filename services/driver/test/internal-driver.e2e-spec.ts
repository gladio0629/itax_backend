import { INestApplication } from '@nestjs/common';
import request from 'supertest';
import { createHarness, Harness, INTERNAL_TOKEN } from './support/helpers';

const USER = '11111111-1111-4111-8111-111111111111';

describe('GET /internal/driver/drivers/:id (e2e)', () => {
  let h: Harness;
  let app: INestApplication;
  const get = (id: string, token: string | null = INTERNAL_TOKEN) => {
    const r = request(app.getHttpServer()).get(`/internal/driver/drivers/${id}`);
    return token === null ? r : r.set('X-Internal-Token', token);
  };

  beforeEach(async () => { h = await createHarness(); app = h.app; });
  afterEach(async () => { await app.close(); });

  it('haydovchi ma\'lumotini qaytaradi', async () => {
    const d = await h.store.ensureDriver(USER, '+998901234567');
    await h.store.updateProfile(d.id, { firstName: 'Ali', lastName: 'Valiyev' });
    await h.store.upsertCar({ driverId: d.id, brand: 'Chevrolet', model: 'Cobalt', year: 2020, color: 'Oq', plateNumber: '01A123BC', carClass: 'economy', seats: 4 });

    const res = await get(d.id);
    expect(res.status).toBe(200);
    expect(res.body).toEqual({
      id: d.id, phone: '+998901234567', firstName: 'Ali', lastName: 'Valiyev', status: 'draft',
      vehiclePlate: '01A123BC', vehicleModel: 'Chevrolet Cobalt', rating: 0,
    });
  });

  it('mashina yo\'q bo\'lsa vehicle maydonlari null', async () => {
    const d = await h.store.ensureDriver(USER);
    const res = await get(d.id);
    expect(res.status).toBe(200);
    expect(res.body.vehiclePlate).toBeNull();
    expect(res.body.vehicleModel).toBeNull();
  });

  it('token yo\'q yoki noto\'g\'ri — 403', async () => {
    const d = await h.store.ensureDriver(USER);
    for (const t of [null, '', 'wrong-token', INTERNAL_TOKEN + 'x', INTERNAL_TOKEN.slice(0, -1)]) {
      const res = await get(d.id, t);
      expect(res.status).toBe(403);
      expect(res.body).toEqual({ error: { key: 'FORBIDDEN', params: { reason: 'internal_only' } } });
    }
  });

  it('foydalanuvchi JWT i bilan kirib bo\'lmaydi', async () => {
    const d = await h.store.ensureDriver(USER);
    const { makeToken } = await import('./support/helpers');
    const res = await request(app.getHttpServer()).get(`/internal/driver/drivers/${d.id}`)
      .set('Authorization', `Bearer ${makeToken()}`);
    expect(res.status).toBe(403);
  });

  it('mavjud bo\'lmagan id — 404 DRIVER_NOT_FOUND', async () => {
    const id = '99999999-9999-4999-8999-999999999999';
    const res = await get(id);
    expect(res.status).toBe(404);
    expect(res.body).toEqual({ error: { key: 'DRIVER_NOT_FOUND', params: { driver_id: id } } });
  });

  it('UUID bo\'lmagan id — 422', async () => {
    const res = await get('not-a-uuid');
    expect(res.status).toBe(422);
    expect(res.body.error.key).toBe('VALIDATION_ERROR');
  });
});

describe('internal token — sozlanmagan bo\'lsa fail closed', () => {
  it('INTERNAL_TOKENS bo\'sh — hamma so\'rov rad etiladi', async () => {
    const { InternalTokenGuard } = await import('../src/auth/internal-token.guard');
    const guard = new InternalTokenGuard({ internalTokens: [] } as never);
    const ctx = { switchToHttp: () => ({ getRequest: () => ({ headers: { 'x-internal-token': 'anything' } }) }) } as never;
    expect(() => guard.canActivate(ctx)).toThrow();
  });
});
