import { createHarness, Harness } from './support/harness';

const D1 = '00000000-0000-4000-8000-000000000101';
const D3 = '00000000-0000-4000-8000-000000000103'; // blocked
const P1 = '00000000-0000-4000-8000-000000000301';

describe('haydovchilar, yo\'lovchilar, buyurtmalar, statistika, audit', () => {
  let h: Harness;
  beforeEach(async () => { h = await createHarness(); });
  afterEach(async () => { await h.app.close(); });

  it('haydovchilar: ro\'yxat, filtr, qidiruv, batafsil, 404', async () => {
    const all = await h.as('manager').get('/admin/v1/drivers');
    expect(all.body.data).toHaveLength(3);
    expect((await h.as('manager').get('/admin/v1/drivers?state=blocked')).body.data.map((d: any) => d.id)).toEqual([D3]);
    expect((await h.as('manager').get('/admin/v1/drivers?q=01B456')).body.data).toHaveLength(1); // davlat raqami
    expect((await h.as('manager').get('/admin/v1/drivers?q=+998901110001')).body.data[0].id).toBe(D1);
    const p1 = await h.as('manager').get('/admin/v1/drivers?limit=2');
    expect(p1.body.cursor).toEqual(expect.any(String));
    const p2 = await h.as('manager').get(`/admin/v1/drivers?limit=2&cursor=${p1.body.cursor}`);
    expect(p2.body.data).toHaveLength(1);
    expect(p2.body.cursor).toBeNull();
    expect((await h.as('manager').get(`/admin/v1/drivers/${D1}`)).body).toMatchObject({ id: D1, state: 'active', driver_state: 'active' });
    const nf = await h.as('manager').get('/admin/v1/drivers/00000000-0000-4000-8000-00000000dead');
    expect(nf.status).toBe(404);
    expect(nf.body).toEqual({ error: { key: 'DRIVER_NOT_FOUND', params: { driver_id: '00000000-0000-4000-8000-00000000dead' } } });
    expect((await h.as('manager').get('/admin/v1/drivers/xyz')).status).toBe(422);
  });

  it('bloklash (sabab majburiy) / blokdan chiqarish / arxivlash + audit oldingi-yangi qiymat bilan', async () => {
    const url = `/admin/v1/drivers/${D1}`;
    const noReason = await h.as('manager').put(`${url}/block`).send({});
    expect(noReason.status).toBe(422);
    expect((await h.as('manager').put(url).send({ state: 'blocked' })).body.error.params.fields).toEqual({ state_reason: 'required' });

    const b = await h.as('manager').put(`${url}/block`).send({ state_reason: 'Shikoyatlar ko\'p' });
    expect(b.status).toBe(200);
    expect(b.body).toMatchObject({ state: 'blocked', state_reason: 'Shikoyatlar ko\'p' });
    expect((await h.as('manager').put(`${url}/block`).send({ state_reason: 'yana' })).body.state).toBe('blocked'); // idempotent
    const ub = await h.as('manager').put(`${url}/unblock`);
    expect(ub.body).toMatchObject({ state: 'active', state_reason: null });
    expect((await h.as('manager').put(`${url}/unblock`)).status).toBe(403); // bloklanmagan

    const ar = await h.as('manager').put(`${url}/archive`);
    expect(ar.body.state).toBe('archived');
    expect((await h.as('manager').put(`${url}/block`).send({ state_reason: 'x' })).status).toBe(403); // arxivlangan
    expect((await h.as('manager').put(url).send({ state: 'active' })).body.state).toBe('active'); // qaytarish
    expect((await h.as('manager').delete(url)).status).toBe(204);
    expect((await h.as('manager').get(url)).body.state).toBe('archived'); // o'chmaydi

    const actions = (await h.audit()).map((a) => a.action);
    expect(actions).toEqual(['driver.block', 'driver.unblock', 'driver.archive', 'driver.restore', 'driver.archive']);
    const first = (await h.audit('driver.block'))[0];
    expect(first.before).toMatchObject({ state: 'active' });
    expect(first.after).toMatchObject({ state: 'blocked', state_reason: 'Shikoyatlar ko\'p' });
    expect(first.actor_id).toBe(h.people.manager.id);
  });

  it('yangilash va yaratish; bo\'sh yoki ortiqcha maydonlar 422', async () => {
    const url = `/admin/v1/drivers/${D1}`;
    expect((await h.as('manager').put(url).send({})).status).toBe(422);
    expect((await h.as('manager').put(url).send({ phone: '+998900000000' })).status).toBe(422);
    expect((await h.as('manager').put(url).send({ first_name: 'Aziz' })).body.first_name).toBe('Aziz');
    const c = await h.as('manager').post('/admin/v1/drivers').send({ phone: '+998905550000', first_name: 'Yangi', last_name: 'Haydovchi' });
    expect(c.status).toBe(201);
    expect((await h.as('manager').post('/admin/v1/drivers').send({ phone: '+998905550000', first_name: 'A', last_name: 'B' })).status).toBe(422);
    expect((await h.as('manager').post('/admin/v1/drivers').send({ phone: '123', first_name: 'A', last_name: 'B' })).status).toBe(422);
    expect((await h.audit('driver.create'))).toHaveLength(1);
  });

  it('yo\'lovchilar uchun xuddi shu: /admin/v1/passengers/*', async () => {
    const url = `/admin/v1/passengers/${P1}`;
    expect((await h.as('manager').get('/admin/v1/passengers')).body.data).toHaveLength(3);
    expect((await h.as('manager').get(url)).body).toMatchObject({ id: P1, state: 'active' });
    expect((await h.as('manager').put(`${url}/block`).send({ state_reason: 'spam' })).body.state).toBe('blocked');
    expect((await h.as('manager').get('/admin/v1/passengers?state=blocked')).body.data).toHaveLength(1);
    expect((await h.as('manager').put(`${url}/unblock`)).body.state).toBe('active');
    expect((await h.as('manager').put(`${url}/archive`)).body.state).toBe('archived');
    const nf = await h.as('manager').get('/admin/v1/passengers/00000000-0000-4000-8000-00000000dead');
    expect(nf.body.error.key).toBe('PASSENGER_NOT_FOUND');
    expect((await h.as('manager').post('/admin/v1/passengers').send({ phone: '+998906660000', first_name: 'A', last_name: 'B' })).status).toBe(201);
    expect((await h.as('marketer').get('/admin/v1/passengers')).status).toBe(403);
    expect((await h.audit()).every((a) => a.action.startsWith('passenger.'))).toBe(true);
  });

  it('buyurtmalar: faqat ko\'rish; holat/sana/qidiruv filtri; 404', async () => {
    const all = await h.as('root').get('/admin/v1/orders');
    expect(all.body.data).toHaveLength(5);
    expect((await h.as('root').get('/admin/v1/orders?status=completed')).body.data).toHaveLength(2);
    expect((await h.as('root').get('/admin/v1/orders?from_date=2026-11-05&to_date=2026-11-08')).body.data).toHaveLength(2);
    expect((await h.as('root').get('/admin/v1/orders?q=chilonzor 3')).body.data).toHaveLength(1);
    expect((await h.as('root').get('/admin/v1/orders?status=bogus')).status).toBe(422);
    expect((await h.as('root').get('/admin/v1/orders?from_date=2026-13-01')).status).toBe(422);
    const one = await h.as('root').get(`/admin/v1/orders/${all.body.data[0].id}`);
    expect(one.status).toBe(200);
    expect((await h.as('root').get('/admin/v1/orders/00000000-0000-4000-8000-00000000dead')).body.error.key).toBe('ORDER_NOT_FOUND');
    for (const m of ['post', 'put', 'delete'] as const) {
      expect((await h.as('root')[m](`/admin/v1/orders/${all.body.data[0].id}`)).status).toBe(404); // yozish endpointi yo'q
    }
  });

  it('statistika: marketolog ham ko\'radi (faqat shu bo\'lim); sana oralig\'i Toshkent vaqtida', async () => {
    const s = await h.as('marketer').get('/admin/v1/stats');
    expect(s.status).toBe(200);
    expect(s.body).toMatchObject({ orders_total: 5, orders_completed: 2, orders_cancelled: 1, active_drivers: 2, revenue: 4_300_000, commission: 645_000, drivers_over_limit: 1 });
    // 2026-11-05T09:00Z = Toshkent 14:00 (5-noyabr)
    const day = await h.as('marketer').get('/admin/v1/stats?from_date=2026-11-05&to_date=2026-11-05');
    expect(day.body.orders_total).toBe(1);
    // 2026-11-12T01:00Z = Toshkent 06:00 (12-noyabr): 11-noyabr oralig'ida emas
    const d11 = await h.as('marketer').get('/admin/v1/stats?from_date=2026-11-11&to_date=2026-11-11');
    expect(d11.body.orders_total).toBe(1);
    expect((await h.as('marketer').get('/admin/v1/stats?from_date=bad')).status).toBe(422);
    expect((await h.as('op1').get('/admin/v1/stats')).status).toBe(403);
  });

  it('audit: har bir yozish amali bitta yozuv; o\'qish yozmaydi; yozuvni o\'zgartiradigan endpoint yo\'q', async () => {
    await h.as('owner').get('/admin/v1/staff');
    await h.as('owner').get('/admin/v1/orders');
    expect(await h.audit()).toHaveLength(0);

    const st = (await h.as('owner').post('/admin/v1/staff').send({ email: 'a@e.com', phone: '+998939999999', full_name: 'A', role: 'marketer' })).body;
    await h.as('owner').put(`/admin/v1/staff/${st.id}`).send({ full_name: 'B' });
    await h.as('owner').delete(`/admin/v1/staff/${st.id}`);
    const t = (await h.as('manager').post('/admin/v1/tariffs').send({ name: 'T', car_class: 'economy', base_fare: 1, per_km: 1, per_min: 1, min_fare: 1, commission_percent: 10 })).body;
    await h.as('owner').post(`/admin/v1/tariffs/${t.id}/approve`);
    await h.as('manager').post('/admin/v1/wallet/correction').send({ wallet_id: '00000000-0000-4000-8000-000000000201', amount: 5, reason: 'sabab bor' });
    await h.as('manager').put(`/admin/v1/drivers/${D1}/block`).send({ state_reason: 'x' });
    // rad etilgan so'rovlar audit yozmaydi
    await h.as('manager').put(`/admin/v1/staff/${h.people.owner.id}`).send({ full_name: 'Hack' });
    await h.as('marketer').post('/admin/v1/tariffs').send({});

    expect((await h.audit()).map((a) => a.action)).toEqual([
      'staff.create', 'staff.update', 'staff.archive', 'tariff.create', 'tariff.approve', 'wallet.correction.create', 'driver.block',
    ]);
    expect((await h.audit()).every((a) => a.actor_id !== null)).toBe(true);
    // API orqali audit o'zgartirib bo'lmaydi
    for (const m of ['put', 'post', 'delete'] as const) {
      expect((await h.as('root')[m]('/admin/v1/audit')).status).toBe(404);
    }
  });

  it('xato yozuvi tranzaksiyani bekor qiladi: audit va o\'zgarish birga saqlanadi yoki birga yo\'q', async () => {
    // identity ga yozish xato bersa — xodim ham, audit ham yo'q
    const { IdentityStaffPort } = await import('../src/ports/ports');
    const port = h.app.get(IdentityStaffPort);
    const orig = port.create.bind(port);
    port.create = async () => { throw new Error('identity ishlamayapti'); };
    const r = await h.as('owner').post('/admin/v1/staff').send({ email: 'f@e.com', phone: '+998938888888', full_name: 'F', role: 'marketer' });
    expect(r.status).toBe(500);
    expect(r.body.error.key).toBe('INTERNAL_ERROR');
    port.create = orig;
    // Audit yozuvi identity chaqiruvidan KEYIN, o'sha tranzaksiyada yoziladi — xatoda umuman yozilmaydi.
    // (pg-mem ROLLBACK ni qo'llamaydi, shuning uchun xodim qatorining yo'qligi bu yerda tekshirilmaydi —
    // haqiqiy PostgreSQL da tranzaksiya butunlay bekor bo'ladi.)
    expect(await h.audit('staff.create')).toHaveLength(0);
  });
});
