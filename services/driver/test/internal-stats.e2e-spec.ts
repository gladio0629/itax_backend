import { INestApplication } from '@nestjs/common';
import request from 'supertest';
import type { DriverState } from '../src/drivers/entities/driver.entity';
import { createHarness, Harness, INTERNAL_TOKEN, makeToken } from './support/helpers';

describe('GET /internal/driver/stats (e2e)', () => {
  let h: Harness;
  let app: INestApplication;
  const get = (query = '', token: string | null = INTERNAL_TOKEN) => {
    const r = request(app.getHttpServer()).get(`/internal/driver/stats${query}`);
    return token === null ? r : r.set('X-Internal-Token', token);
  };

  let n = 0;
  async function seed(state: DriverState, createdAt: string) {
    n += 1;
    const d = await h.store.ensureDriver(`cccccccc-0000-4000-8000-${String(n).padStart(12, '0')}`);
    d.state = state;
    d.createdAt = new Date(createdAt);
  }

  beforeEach(async () => { h = await createHarness(); app = h.app; });
  afterEach(async () => { await app.close(); });

  it('haydovchi yo\'q — hamma holat 0 bilan', async () => {
    const r = await get();
    expect(r.status).toBe(200);
    expect(r.body).toEqual({
      total: 0,
      by_state: { draft: 0, pending_review: 0, active: 0, rejected: 0, blocked: 0 },
      new_in_range: 0,
    });
  });

  it('holatlar bo\'yicha soni va oraliq [from 00:00, to+1 00:00) UTC', async () => {
    await seed('draft', '2026-10-31T23:59:59.999Z');      // oraliqdan oldin
    await seed('active', '2026-11-01T00:00:00.000Z');     // from chegarasi — kiradi
    await seed('active', '2026-11-05T12:00:00.000Z');
    await seed('pending_review', '2026-11-10T23:59:59.999Z'); // to kunining oxiri — kiradi
    await seed('blocked', '2026-11-11T00:00:00.000Z');    // to+1 00:00 — kirmaydi
    await seed('rejected', '2026-12-01T00:00:00.000Z');

    const all = await get();
    expect(all.body).toEqual({
      total: 6,
      by_state: { draft: 1, pending_review: 1, active: 2, rejected: 1, blocked: 1 },
      new_in_range: 6, // oraliq yo'q → total
    });

    const ranged = await get('?from_date=2026-11-01&to_date=2026-11-10');
    expect(ranged.status).toBe(200);
    expect(ranged.body).toEqual({ ...all.body, new_in_range: 3 });

    // bir kunlik oraliq
    expect((await get('?from_date=2026-11-01&to_date=2026-11-01')).body.new_in_range).toBe(1);
    // faqat bitta chegara — ikkinchi tomoni ochiq
    expect((await get('?from_date=2026-11-11')).body.new_in_range).toBe(2);
    expect((await get('?to_date=2026-10-31')).body.new_in_range).toBe(1);
  });

  it('noto\'g\'ri sana / ortiqcha parametr → 422', async () => {
    for (const [q, expected] of [
      ['?from_date=2026-02-31', { from_date: 'invalid_date' }],
      ['?to_date=2026-13-01', { to_date: 'invalid_date' }],
      ['?from_date=01.11.2026', { from_date: 'invalid_date' }],
      ['?from_date=', { from_date: 'invalid_date' }],
      ['?from_date=2026-11-01T00:00:00Z', { from_date: 'invalid_date' }],
      ['?from_date=x&to_date=y', { from_date: 'invalid_date', to_date: 'invalid_date' }],
      ['?from_date=2026-11-02&to_date=2026-11-01', { to_date: 'before_from_date' }],
    ] as const) {
      const r = await get(q);
      expect(r.status).toBe(422);
      expect(r.body).toEqual({ error: { key: 'VALIDATION_ERROR', params: { fields: expected } } });
    }
    const arr = await get('?from_date=2026-11-01&from_date=2026-11-02');
    expect(arr.status).toBe(422);
    const extra = await get('?state=active');
    expect(extra.status).toBe(422);
    expect(extra.body.error.params.fields.state).toBeDefined();
  });

  it('X-Internal-Token yo\'q yoki noto\'g\'ri — 403; foydalanuvchi JWT i ham o\'tmaydi', async () => {
    for (const t of [null, '', 'wrong-token', INTERNAL_TOKEN + 'x']) {
      const r = await get('', t);
      expect(r.status).toBe(403);
      expect(r.body).toEqual({ error: { key: 'FORBIDDEN', params: { reason: 'internal_only' } } });
    }
    const jwt = await request(app.getHttpServer()).get('/internal/driver/stats').set('Authorization', `Bearer ${makeToken()}`);
    expect(jwt.status).toBe(403);
  });
});
