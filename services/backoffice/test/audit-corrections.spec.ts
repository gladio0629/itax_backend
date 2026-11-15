import { createHarness, Harness } from './support/harness';

const W1 = '00000000-0000-4000-8000-000000000201';
const D1 = '00000000-0000-4000-8000-000000000101';

describe('tuzatishlar ro\'yxati: GET /admin/v1/wallet/correction', () => {
  let h: Harness;
  beforeEach(async () => { h = await createHarness(); });
  afterEach(async () => { await h.app.close(); });
  const create = (who: string, amount: number) =>
    h.as(who).post('/admin/v1/wallet/correction').send({ wallet_id: W1, amount, reason: 'Sabab yozildi' }).expect(202);

  it('yangilari birinchi, approval_status filtri, created_by_name, sahifalash', async () => {
    const a = (await create('manager', 100)).body;
    const b = (await create('manager2', 200)).body;
    const c = (await create('owner', 300)).body;
    await h.as('owner').post(`/admin/v1/wallet/correction/${a.id}/approve`).expect(200);
    await h.as('manager').post(`/admin/v1/wallet/correction/${b.id}/reject`).send({ reason: 'kerak emas' }).expect(200);

    const all = await h.as('root').get('/admin/v1/wallet/correction');
    expect(all.status).toBe(200);
    expect(all.body.data.map((x: any) => x.id)).toEqual([c.id, b.id, a.id]);
    expect(all.body.cursor).toBeNull();
    expect(all.body.data[0]).toEqual({
      id: c.id, wallet_id: W1, amount: 300, reason: 'Sabab yozildi', approval_status: 'pending_approval',
      created_by: h.people.owner.id, created_by_name: 'owner', approved_by: null, reject_reason: null,
      created_at: expect.any(String),
    });

    const pending = await h.as('root').get('/admin/v1/wallet/correction?approval_status=pending_approval');
    expect(pending.body.data.map((x: any) => x.id)).toEqual([c.id]);
    const rejected = await h.as('root').get('/admin/v1/wallet/correction?approval_status=rejected');
    expect(rejected.body.data).toEqual([expect.objectContaining({ id: b.id, reject_reason: 'kerak emas', approved_by: h.people.manager.id })]);
    expect((await h.as('root').get('/admin/v1/wallet/correction?approval_status=approved')).body.data[0].id).toBe(a.id);

    const p1 = await h.as('root').get('/admin/v1/wallet/correction?limit=2');
    expect(p1.body.data).toHaveLength(2);
    const p2 = await h.as('root').get(`/admin/v1/wallet/correction?limit=2&cursor=${p1.body.cursor}`);
    expect(p2.body.data.map((x: any) => x.id)).toEqual([a.id]);
    expect(p2.body.cursor).toBeNull();
  });

  it('noto\'g\'ri filtr / noma\'lum parametr — 422; ruxsat wallet:read', async () => {
    expect((await h.as('root').get('/admin/v1/wallet/correction?approval_status=bogus')).status).toBe(422);
    expect((await h.as('root').get('/admin/v1/wallet/correction?foo=1')).status).toBe(422);
    expect((await h.as('root').get('/admin/v1/wallet/correction?cursor=xyz')).status).toBe(422);
    expect((await h.as('marketer').get('/admin/v1/wallet/correction')).status).toBe(403);
    expect((await h.as('op1').get('/admin/v1/wallet/correction')).status).toBe(403);
  });
});

describe('audit jurnali: GET /admin/v1/audit', () => {
  let h: Harness;
  beforeEach(async () => { h = await createHarness(); });
  afterEach(async () => { await h.app.close(); });

  it('yangilari birinchi; to\'liq shakl; filtrlar; sahifalash; faqat o\'qish', async () => {
    const c = (await h.as('manager').post('/admin/v1/wallet/correction').set('X-Request-Id', 'req-1')
      .send({ wallet_id: W1, amount: 5, reason: 'sabab bor' })).body;
    await h.as('owner').put(`/admin/v1/drivers/${D1}/block`).send({ state_reason: 'x' }).expect(200);
    await h.as('owner').put(`/admin/v1/drivers/${D1}/unblock`).expect(200);

    const all = await h.as('root').get('/admin/v1/audit');
    expect(all.status).toBe(200);
    expect(all.body.data.map((a: any) => a.action)).toEqual(['driver.unblock', 'driver.block', 'wallet.correction.create']);
    expect(all.body.data[2]).toEqual({
      id: expect.any(String), actor_id: h.people.manager.id, actor_role: 'manager', action: 'wallet.correction.create',
      entity_type: 'correction', entity_id: c.id, before: null, after: expect.objectContaining({ id: c.id, amount: 5 }),
      request_id: 'req-1', created_at: expect.any(String),
    });

    const q = (s: string) => h.as('owner').get(`/admin/v1/audit?${s}`);
    expect((await q('entity_type=driver')).body.data).toHaveLength(2);
    expect((await q(`entity_type=driver&entity_id=${D1}&action=driver.block`)).body.data).toHaveLength(1);
    expect((await q(`actor_id=${h.people.manager.id}`)).body.data.map((a: any) => a.entity_id)).toEqual([c.id]);
    expect((await q(`actor_id=${h.people.op1.id}`)).body.data).toEqual([]);
    const today = new Date(Date.now() + 5 * 3600_000).toISOString().slice(0, 10);
    expect((await q(`from_date=${today}&to_date=${today}`)).body.data).toHaveLength(3);
    expect((await q('to_date=2020-01-01')).body.data).toHaveLength(0);

    const p1 = await q('limit=2');
    expect(p1.body.data).toHaveLength(2);
    const p2 = await q(`limit=2&cursor=${p1.body.cursor}`);
    expect(p2.body.data.map((a: any) => a.action)).toEqual(['wallet.correction.create']);
    expect(p2.body.cursor).toBeNull();

    // o'qish audit yozmaydi
    expect(await h.audit()).toHaveLength(3);
  });

  it('validatsiya 422; ruxsat faqat superadmin va owner', async () => {
    expect((await h.as('root').get('/admin/v1/audit?actor_id=abc')).status).toBe(422);
    expect((await h.as('root').get('/admin/v1/audit?from_date=2026-02-30')).status).toBe(422);
    expect((await h.as('root').get('/admin/v1/audit?from_date=2026-11-10&to_date=2026-11-01')).status).toBe(422);
    expect((await h.as('root').get('/admin/v1/audit?nope=1')).status).toBe(422);
    expect((await h.as('owner').get('/admin/v1/audit')).status).toBe(200);
    for (const who of ['manager', 'marketer', 'op1']) {
      expect((await h.as(who).get('/admin/v1/audit')).status).toBe(403);
    }
  });
});
