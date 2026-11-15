import { createHarness, Harness } from './support/harness';

describe('xodimlar CRUD', () => {
  let h: Harness;
  beforeAll(async () => { h = await createHarness(); });
  afterAll(async () => { await h.app.close(); });
  const body = (o: object = {}) => ({ email: 'x@example.com', phone: '+998931234567', full_name: 'Test Xodim', role: 'marketer', ...o });

  it('yaratish: creatable_roles bo\'yicha, identity ga ham yoziladi, audit', async () => {
    const r = await h.as('manager').post('/admin/v1/staff').send(body());
    expect(r.status).toBe(201);
    expect(r.body).toMatchObject({ email: 'x@example.com', role: 'marketer', state: 'active', phone: '+998931234567' });
    expect(h.mock.identityStaff.get(r.body.id)).toMatchObject({ role: 'marketer', active: true, email: 'x@example.com' });
    const a = await h.audit('staff.create');
    expect(a).toHaveLength(1);
    expect(a[0]).toMatchObject({ actor_id: h.people.manager.id, entity_id: r.body.id, before: null });
    expect(a[0].after).toMatchObject({ role: 'marketer', email: 'x@example.com' });
  });

  it('o\'zi yarata olmaydigan rol → 403 (manager owner/manager/superadmin yarata olmaydi)', async () => {
    for (const role of ['superadmin', 'owner', 'manager']) {
      const r = await h.as('manager').post('/admin/v1/staff').send(body({ role, email: `${role}@e.com`, phone: '+998930000001' }));
      expect(r.status).toBe(403);
      expect(r.body).toEqual({ error: { key: 'FORBIDDEN', params: { reason: 'role_not_creatable' } } });
    }
    expect((await h.as('owner').post('/admin/v1/staff').send(body({ role: 'owner', email: 'o2@e.com', phone: '+998930000002' }))).status).toBe(403);
    expect((await h.as('root').post('/admin/v1/staff').send(body({ role: 'owner', email: 'o3@e.com', phone: '+998930000003' }))).status).toBe(201);
    expect((await h.as('marketer').post('/admin/v1/staff').send(body())).status).toBe(403);
  });

  it('telefon majburiy; email operatordan boshqa hamma uchun majburiy', async () => {
    const noPhone = await h.as('owner').post('/admin/v1/staff').send({ email: 'n@e.com', full_name: 'N', role: 'marketer' });
    expect(noPhone.status).toBe(422);
    expect(noPhone.body.error.params.fields.phone).toBeDefined();
    const bad = await h.as('owner').post('/admin/v1/staff').send(body({ phone: '901234567', email: 'n2@e.com' }));
    expect(bad.status).toBe(422);
    const noEmail = await h.as('owner').post('/admin/v1/staff').send({ phone: '+998930000010', full_name: 'M', role: 'marketer' });
    expect(noEmail.status).toBe(422);
    expect(noEmail.body.error.params.fields).toEqual({ email: 'required' });
    const op = await h.as('owner').post('/admin/v1/staff').send({ phone: '+998930000011', full_name: 'Op', role: 'operator' });
    expect(op.status).toBe(201);
    expect(op.body.email).toBeNull();
    const extra = await h.as('owner').post('/admin/v1/staff').send(body({ email: 'e@e.com', phone: '+998930000012', state: 'blocked' }));
    expect(extra.status).toBe(422); // ortiqcha maydon
  });

  it('takroriy email/telefon → 409', async () => {
    await h.as('owner').post('/admin/v1/staff').send(body({ email: 'dup@e.com', phone: '+998930000020' })).expect(201);
    const e = await h.as('owner').post('/admin/v1/staff').send(body({ email: 'DUP@e.com', phone: '+998930000021' }));
    expect(e.status).toBe(409);
    expect(e.body.error.key).toBe('STAFF_EMAIL_TAKEN');
    const p = await h.as('owner').post('/admin/v1/staff').send(body({ email: 'dup2@e.com', phone: '+998930000020' }));
    expect(p.status).toBe(409);
    expect(p.body.error.key).toBe('STAFF_PHONE_TAKEN');
  });

  it('tahrirlash faqat o\'zi yarata oladigan rollardagi xodimlar uchun', async () => {
    const op = (await h.as('manager').post('/admin/v1/staff').send({ phone: '+998930000030', full_name: 'Op A', role: 'operator' })).body;
    const upd = await h.as('manager').put(`/admin/v1/staff/${op.id}`).send({ full_name: 'Op B', state: 'blocked' });
    expect(upd.status).toBe(200);
    expect(upd.body).toMatchObject({ full_name: 'Op B', state: 'blocked' });
    expect(h.mock.identityStaff.get(op.id)!.active).toBe(false);
    const a = (await h.audit('staff.update')).find((x) => x.entity_id === op.id)!;
    expect(a.before.full_name).toBe('Op A');
    expect(a.after.full_name).toBe('Op B');

    // manager boshqa manager'ni (o'z roli — creatable emas) tahrirlay olmaydi
    const r = await h.as('manager').put(`/admin/v1/staff/${h.people.manager2.id}`).send({ full_name: 'Hack' });
    expect(r.status).toBe(403);
    // owner'ni ko'rmaydi ham
    expect((await h.as('manager').put(`/admin/v1/staff/${h.people.owner.id}`).send({ full_name: 'Hack' })).status).toBe(404);
    expect((await h.as('manager').get(`/admin/v1/staff/${h.people.owner.id}`)).body.error.key).toBe('STAFF_NOT_FOUND');
    // rolni yarata olmaydigan rolga o'zgartirish mumkin emas
    const esc = await h.as('manager').put(`/admin/v1/staff/${op.id}`).send({ role: 'owner' });
    expect(esc.status).toBe(403);
    expect((await h.as('manager').put(`/admin/v1/staff/${op.id}`).send({ role: 'marketer' })).status).toBe(422); // operatorda email yo'q
    expect((await h.as('manager').put(`/admin/v1/staff/${op.id}`).send({})).status).toBe(422);
    expect((await h.as('manager').put(`/admin/v1/staff/${op.id}`).send({ email: 'z@e.com' })).status).toBe(422); // email o'zgarmaydi
    expect((await h.as('manager').put(`/admin/v1/staff/${op.id}`).send({ state: 'archived' })).status).toBe(422);
  });

  it('o\'z rolini/holatini o\'zgartirish va o\'zini arxivlash taqiqlangan', async () => {
    expect((await h.as('root').put(`/admin/v1/staff/${h.people.root.id}`).send({ role: 'owner' })).status).toBe(403);
    expect((await h.as('root').put(`/admin/v1/staff/${h.people.root.id}`).send({ state: 'blocked' })).status).toBe(403);
    expect((await h.as('root').delete(`/admin/v1/staff/${h.people.root.id}`)).status).toBe(403);
    expect((await h.as('root').put(`/admin/v1/staff/${h.people.root.id}`).send({ full_name: 'Yangi Ism' })).status).toBe(200);
  });

  it('DELETE = arxivlash (soft delete): yozuv qoladi, PUT state=active bilan qaytariladi', async () => {
    const m = (await h.as('owner').post('/admin/v1/staff').send(body({ email: 'arch@e.com', phone: '+998930000040' }))).body;
    const del = await h.as('owner').delete(`/admin/v1/staff/${m.id}`);
    expect(del.status).toBe(204);
    const got = await h.as('owner').get(`/admin/v1/staff/${m.id}`);
    expect(got.body.state).toBe('archived');
    expect((await h.ds.query(`SELECT count(*)::int AS n FROM staff WHERE id = '${m.id}'`))[0].n).toBe(1);
    expect(h.mock.identityStaff.get(m.id)!.active).toBe(false);
    expect((await h.as('owner').delete(`/admin/v1/staff/${m.id}`)).status).toBe(204); // idempotent
    const back = await h.as('owner').put(`/admin/v1/staff/${m.id}`).send({ state: 'active' });
    expect(back.body.state).toBe('active');
    expect(h.mock.identityStaff.get(m.id)!.active).toBe(true);
    expect((await h.audit('staff.archive')).filter((a) => a.entity_id === m.id)).toHaveLength(1);
    expect((await h.as('owner').delete('/admin/v1/staff/00000000-0000-4000-8000-00000000dead')).status).toBe(404);
  });

  it('oxirgi faol superadminni bloklab/arxivlab bo\'lmaydi', async () => {
    const second = (await h.as('root').post('/admin/v1/staff').send(body({ role: 'superadmin', email: 'sa2@e.com', phone: '+998930000050' }))).body;
    // ikkita faol superadmin bor: root ikkinchisini arxivlay oladi...
    expect((await h.as('root').delete(`/admin/v1/staff/${second.id}`)).status).toBe(204);
    // ...tiklab, endi root'ni ikkinchisi bloklashga urinadi: root yagona boshqa faol emas — ikkinchi faol emas, shuning uchun
    await h.as('root').put(`/admin/v1/staff/${second.id}`).send({ state: 'active' }).expect(200);
    await h.ds.query(`UPDATE staff SET state = 'blocked' WHERE id = '${h.people.root.id}'`); // root faol emas
    const { makeToken } = await import('./support/harness');
    const t = `Bearer ${makeToken(second.id)}`;
    const r = await h.http().delete(`/admin/v1/staff/${second.id}`).set('Authorization', t);
    expect(r.status).toBe(403); // o'zini arxivlash
    await h.ds.query(`UPDATE staff SET state = 'active' WHERE id = '${h.people.root.id}'`);
  });

  it('ro\'yxat: rol/holat/qidiruv filtri va cursor pagination; ko\'rinmaydigan rollar yo\'q', async () => {
    for (let i = 0; i < 5; i++) {
      await h.as('owner').post('/admin/v1/staff').send(body({ email: `pg${i}@e.com`, phone: `+99893100000${i}`, full_name: `Pag ${i}` })).expect(201);
    }
    const seen: string[] = [];
    let cursor: string | null = null;
    let pages = 0;
    do {
      const r: any = await h.as('owner').get(`/admin/v1/staff?role=marketer&q=pag&limit=2${cursor ? `&cursor=${cursor}` : ''}`);
      expect(r.status).toBe(200);
      seen.push(...r.body.data.map((s: any) => s.id));
      cursor = r.body.cursor;
      pages++;
    } while (cursor);
    expect(pages).toBe(3);
    expect(new Set(seen).size).toBe(5);

    const blocked = await h.as('owner').get('/admin/v1/staff?state=blocked');
    expect(blocked.body.data.every((s: any) => s.state === 'blocked')).toBe(true);
    const mgr = await h.as('manager').get('/admin/v1/staff?limit=100');
    expect(mgr.body.data.map((s: any) => s.role).sort().filter((r: string, i: number, a: string[]) => a.indexOf(r) === i))
      .toEqual(expect.not.arrayContaining(['owner', 'superadmin']));
    expect((await h.as('owner').get('/admin/v1/staff?role=bogus')).status).toBe(422);
    expect((await h.as('owner').get('/admin/v1/staff?cursor=%25%25')).body.error.params.fields).toEqual({ cursor: 'invalid' });
    expect((await h.as('owner').get('/admin/v1/staff?limit=1000')).status).toBe(422);
  });
});
