import { createHarness, Harness } from './support/harness';

const W1 = '00000000-0000-4000-8000-000000000201'; // mock hamyon (balans 150 000)

describe('ikki kishi tasdig\'i: hamyon tuzatishlari', () => {
  let h: Harness;
  beforeEach(async () => { h = await createHarness(); });
  afterEach(async () => { await h.app.close(); });
  const create = (who = 'manager', extra: object = {}, key?: string) => {
    const r = h.as(who).post('/admin/v1/wallet/correction');
    if (key) r.set('Idempotency-Key', key);
    return r.send({ wallet_id: W1, amount: -25_000, reason: 'Noto\'g\'ri komissiya', ...extra });
  };

  it('yaratish → pending_approval (202), balansga ta\'sir qilmaydi', async () => {
    const r = await create();
    expect(r.status).toBe(202);
    expect(r.body).toMatchObject({ approval_status: 'pending_approval', created_by: h.people.manager.id, approved_by: null, amount: -25000 });
    expect(h.mock.wallets.get(W1)).toBe(150_000);
    expect((await h.as('root').get(`/admin/v1/wallet/ledger?wallet_id=${W1}`)).body.data.some((e: any) => e.kind === 'correction')).toBe(false);
    expect((await h.audit('wallet.correction.create'))).toHaveLength(1);
  });

  it('validatsiya: summa 0 emas, sabab ≥5, hamyon mavjud', async () => {
    expect((await create('manager', { amount: 0 })).status).toBe(422);
    expect((await create('manager', { amount: 1.5 })).status).toBe(422);
    expect((await create('manager', { reason: 'abc' })).status).toBe(422);
    const nf = await create('manager', { wallet_id: '00000000-0000-4000-8000-0000000fffff' });
    expect(nf.body.error.params.fields).toEqual({ wallet_id: 'not_found' });
  });

  it('yaratgan o\'zi tasdiqlay ham, rad eta ham olmaydi (403 FORBIDDEN)', async () => {
    const c = (await create()).body;
    const a = await h.as('manager').post(`/admin/v1/wallet/correction/${c.id}/approve`);
    expect(a.status).toBe(403);
    expect(a.body).toEqual({ error: { key: 'FORBIDDEN', params: { reason: 'self_approval' } } });
    expect((await h.as('manager').post(`/admin/v1/wallet/correction/${c.id}/reject`).send({ reason: 'xato yozildi' })).status).toBe(403);
    expect(h.mock.wallets.get(W1)).toBe(150_000);
  });

  it('ikkinchi xodim tasdiqlaydi → daftarga tushadi va balans o\'zgaradi; qayta tasdiqlab bo\'lmaydi', async () => {
    const c = (await create()).body;
    const a = await h.as('owner').post(`/admin/v1/wallet/correction/${c.id}/approve`);
    expect(a.status).toBe(200);
    expect(a.body).toMatchObject({ approval_status: 'approved', approved_by: h.people.owner.id });
    expect(h.mock.wallets.get(W1)).toBe(125_000);
    const led = await h.as('root').get(`/admin/v1/wallet/ledger?wallet_id=${W1}`);
    const entry = led.body.data.find((e: any) => e.reference_id === c.id);
    expect(entry).toMatchObject({ kind: 'correction', amount: -25000, balance_after: 125_000 });

    const again = await h.as('owner').post(`/admin/v1/wallet/correction/${c.id}/approve`);
    expect(again.status).toBe(403);
    expect(again.body.error.params.reason).toBe('not_pending');
    expect((await h.as('manager2').post(`/admin/v1/wallet/correction/${c.id}/reject`).send({ reason: 'kech qoldingiz' })).body.error.params.reason).toBe('not_pending');
    expect(h.mock.wallets.get(W1)).toBe(125_000);
    expect((await h.audit('wallet.correction.approve'))).toHaveLength(1);
  });

  it('wallet porti xato bersa: 500, audit yozilmaydi, ledger o\'zgarmaydi', async () => {
    const c = (await create()).body;
    const { WalletPort } = await import('../src/ports/ports');
    const port = h.app.get(WalletPort);
    const orig = port.applyCorrection.bind(port);
    port.applyCorrection = async () => { throw new Error('wallet ishlamayapti'); };
    const r = await h.as('owner').post(`/admin/v1/wallet/correction/${c.id}/approve`);
    expect(r.status).toBe(500);
    expect(await h.audit('wallet.correction.approve')).toHaveLength(0);
    expect(h.mock.ledger.some((e) => e.reference_id === c.id)).toBe(false);
    port.applyCorrection = orig; // (pg-mem ROLLBACK ni qo'llamaydi — holat qatori tekshirilmaydi; haqiqiy PG da pending ga qaytadi)
  });

  it('rad etish: sabab majburiy, daftarga tushmaydi', async () => {
    const c = (await create()).body;
    const url = `/admin/v1/wallet/correction/${c.id}/reject`;
    expect((await h.as('owner').post(url).send({})).status).toBe(422);
    expect((await h.as('owner').post(url).send({ reason: 'ab' })).status).toBe(422);
    const r = await h.as('owner').post(url).send({ reason: 'Asossiz tuzatish' });
    expect(r.status).toBe(200);
    expect(r.body).toMatchObject({ approval_status: 'rejected', approved_by: h.people.owner.id, reject_reason: 'Asossiz tuzatish' });
    expect(h.mock.wallets.get(W1)).toBe(150_000);
    expect(h.mock.ledger.some((e) => e.reference_id === c.id)).toBe(false);
    expect((await h.as('owner').post(`/admin/v1/wallet/correction/${c.id}/approve`)).body.error.params.reason).toBe('not_pending');
    expect((await h.as('owner').post('/admin/v1/wallet/correction/00000000-0000-4000-8000-00000000dead/approve')).status).toBe(404);
  });

  it('parallel approve va reject — faqat bittasi yutadi', async () => {
    const c = (await create()).body;
    const [a, r] = await Promise.all([
      h.as('owner').post(`/admin/v1/wallet/correction/${c.id}/approve`),
      h.as('manager2').post(`/admin/v1/wallet/correction/${c.id}/reject`).send({ reason: 'parallel rad' }),
    ]);
    expect([a.status, r.status].sort()).toEqual([200, 403]);
    const row = (await h.ds.query(`SELECT approval_status FROM corrections WHERE id = '${c.id}'`))[0];
    expect(a.status === 200 ? row.approval_status === 'approved' : row.approval_status === 'rejected').toBe(true);
    expect(h.mock.ledger.filter((e) => e.reference_id === c.id).length).toBe(a.status === 200 ? 1 : 0);
  });

  it('Idempotency-Key: takror asl javobni qaytaradi, boshqa tana bilan — 409', async () => {
    const key = '11111111-2222-4333-8444-555555555555';
    const a = await create('manager', {}, key);
    const b = await create('manager', {}, key);
    expect(b.status).toBe(202);
    expect(b.body.id).toBe(a.body.id);
    expect((await h.ds.query('SELECT count(*)::int AS n FROM corrections'))[0].n).toBe(1);
    const c = await create('manager', { amount: -1 }, key);
    expect(c.status).toBe(409);
    expect(c.body.error.key).toBe('IDEMPOTENCY_KEY_REUSED');
    // boshqa xodimning shu kaliti alohida
    expect((await create('manager2', {}, key)).body.id).not.toBe(a.body.id);
    expect((await create('manager', {}, 'not-a-uuid')).status).toBe(422);
  });

  it('ruxsat: marketolog va operator tuzatish yarata/tasdiqlay olmaydi', async () => {
    const c = (await create()).body;
    for (const who of ['marketer', 'op1']) {
      expect((await create(who)).status).toBe(403);
      expect((await h.as(who).post(`/admin/v1/wallet/correction/${c.id}/approve`)).status).toBe(403);
    }
  });
});

describe('ikki kishi tasdig\'i: tariflar', () => {
  let h: Harness;
  beforeEach(async () => { h = await createHarness(); });
  afterEach(async () => { await h.app.close(); });
  const input = (o: object = {}) => ({ name: 'Ekonom', car_class: 'economy', base_fare: 400000, per_km: 150000, per_min: 20000, min_fare: 600000, commission_percent: 15, ...o });
  const make = async (who = 'manager') => (await h.as(who).post('/admin/v1/tariffs').send(input())).body;

  it('yaratish → pending (202), faol emas; yaratgan o\'zi tasdiqlay olmaydi; boshqasi tasdiqlaydi', async () => {
    const r = await h.as('manager').post('/admin/v1/tariffs').send(input());
    expect(r.status).toBe(202);
    expect(r.body).toMatchObject({ active: false, approval_status: 'pending_approval', pending_change: { action: 'create', requested_by: h.people.manager.id } });
    const self = await h.as('manager').post(`/admin/v1/tariffs/${r.body.id}/approve`);
    expect(self.status).toBe(403);
    expect(self.body.error.params.reason).toBe('self_approval');
    const ok = await h.as('owner').post(`/admin/v1/tariffs/${r.body.id}/approve`);
    expect(ok.status).toBe(200);
    expect(ok.body).toMatchObject({ active: true, approval_status: 'approved', pending_change: null, commission_percent: 15 });
    expect((await h.as('owner').post(`/admin/v1/tariffs/${r.body.id}/approve`)).body.error.params.reason).toBe('not_pending');
  });

  it('o\'zgartirish: tasdiqqacha amaldagi qiymatlar o\'zgarmaydi; tasdiqlangach qo\'llanadi', async () => {
    const t = await make();
    await h.as('owner').post(`/admin/v1/tariffs/${t.id}/approve`).expect(200);
    const u = await h.as('manager').put(`/admin/v1/tariffs/${t.id}`).send(input({ base_fare: 500000, commission_percent: 12.5 }));
    expect(u.status).toBe(202);
    expect(u.body).toMatchObject({ base_fare: 400000, commission_percent: 15, approval_status: 'pending_approval', active: true });
    expect(u.body.pending_change).toMatchObject({ action: 'update', values: { base_fare: 500000, commission_percent: 12.5 } });
    // kutayotgan o'zgarish bor — yangisini qabul qilmaydi
    expect((await h.as('manager2').put(`/admin/v1/tariffs/${t.id}`).send(input())).body.error.key).toBe('TARIFF_CHANGE_PENDING');
    expect((await h.as('manager2').delete(`/admin/v1/tariffs/${t.id}`)).status).toBe(409);
    const a = await h.as('manager2').post(`/admin/v1/tariffs/${t.id}/approve`);
    expect(a.body).toMatchObject({ base_fare: 500000, commission_percent: 12.5, approval_status: 'approved', pending_change: null });
  });

  it('rad etish: sabab majburiy, qiymatlar o\'zgarmaydi, pending_change bekor', async () => {
    const t = await make();
    await h.as('owner').post(`/admin/v1/tariffs/${t.id}/approve`).expect(200);
    await h.as('manager').put(`/admin/v1/tariffs/${t.id}`).send(input({ base_fare: 1 })).expect(202);
    const url = `/admin/v1/tariffs/${t.id}/reject`;
    expect((await h.as('owner').post(url).send({})).status).toBe(422);
    expect((await h.as('manager').post(url).send({ reason: 'o\'zi rad etadi' })).status).toBe(403);
    const r = await h.as('owner').post(url).send({ reason: 'Narx juda past' });
    expect(r.body).toMatchObject({ base_fare: 400000, approval_status: 'rejected', pending_change: null, active: true });
    expect((await h.audit('tariff.reject')).at(-1)!.after.reject_reason).toBe('Narx juda past');
    // endi yangi o'zgarish mumkin
    expect((await h.as('manager').put(`/admin/v1/tariffs/${t.id}`).send(input())).status).toBe(202);
  });

  it('o\'chirish: tasdiqlangach soft delete (ro\'yxatdan chiqadi, 404), qator qoladi', async () => {
    const t = await make();
    await h.as('owner').post(`/admin/v1/tariffs/${t.id}/approve`).expect(200);
    const d = await h.as('manager').delete(`/admin/v1/tariffs/${t.id}`);
    expect(d.status).toBe(202);
    expect(d.body.pending_change.action).toBe('delete');
    expect((await h.as('root').get('/admin/v1/tariffs')).body.data.map((x: any) => x.id)).toContain(t.id);
    await h.as('owner').post(`/admin/v1/tariffs/${t.id}/approve`).expect(200);
    expect((await h.as('root').get('/admin/v1/tariffs')).body.data.map((x: any) => x.id)).not.toContain(t.id);
    expect((await h.as('root').get(`/admin/v1/tariffs/${t.id}`)).body.error.key).toBe('TARIFF_NOT_FOUND');
    expect((await h.ds.query(`SELECT count(*)::int AS n FROM tariffs WHERE id = '${t.id}'`))[0].n).toBe(1);
  });

  it('parallel approve — faqat bittasi o\'tadi; validatsiya; ruxsat', async () => {
    const t = await make();
    const [a, b] = await Promise.all([
      h.as('owner').post(`/admin/v1/tariffs/${t.id}/approve`),
      h.as('root').post(`/admin/v1/tariffs/${t.id}/approve`),
    ]);
    expect([a.status, b.status].sort()).toEqual([200, 403]);
    expect((await h.as('manager').post('/admin/v1/tariffs').send(input({ commission_percent: 101 }))).status).toBe(422);
    expect((await h.as('manager').post('/admin/v1/tariffs').send(input({ base_fare: -1 }))).status).toBe(422);
    expect((await h.as('manager').post('/admin/v1/tariffs').send(input({ active: true }))).status).toBe(422);
    expect((await h.as('marketer').get('/admin/v1/tariffs')).status).toBe(403);
    expect((await h.as('op1').post(`/admin/v1/tariffs/${t.id}/approve`)).status).toBe(403);
    expect((await h.as('root').get('/admin/v1/tariffs/00000000-0000-4000-8000-00000000dead')).status).toBe(404);
  });
});
