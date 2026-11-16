import { v4 as uuidv4 } from 'uuid';
import { RedisSessionRevocationPort } from '../src/auth/session-revocation';
import { Errors } from '../src/common/errors';
import { Staff } from '../src/entities/staff.entity';
import { IdentityStaffPort } from '../src/ports/ports';
import { createHarness, Harness, makeToken } from './support/harness';

describe('token va ruxsat tekshiruvi', () => {
  let h: Harness;
  beforeAll(async () => { h = await createHarness(); });
  afterAll(async () => { await h.app.close(); });

  it('token yo\'q / begona kalit / aud=client / refresh → 401|403', async () => {
    const r1 = await h.http().get('/admin/v1/me');
    expect(r1.status).toBe(401);
    expect(r1.body).toEqual({ error: { key: 'AUTH_TOKEN_EXPIRED', params: {} } });
    expect((await h.http().get('/admin/v1/me').set('Authorization', `Bearer ${makeToken(h.people.root.id, {}, true)}`)).status).toBe(401);
    expect((await h.http().get('/admin/v1/me').set('Authorization', `Bearer ${makeToken(h.people.root.id, { typ: 'refresh' })}`)).status).toBe(401);
    expect((await h.http().get('/admin/v1/me').set('Authorization', `Bearer ${makeToken(h.people.root.id, { exp: 1 })}`)).status).toBe(401);
    const wrongAud = await h.http().get('/admin/v1/me').set('Authorization', `Bearer ${makeToken(h.people.root.id, { aud: ['client'] })}`);
    expect(wrongAud.status).toBe(403);
    expect(wrongAud.body.error.key).toBe('FORBIDDEN');
  });

  it('healthz ochiq; /me faqat tokenli xodimga', async () => {
    expect((await h.http().get('/healthz')).text).toBe('ok');
    const me = await h.as('marketer').get('/admin/v1/me');
    expect(me.status).toBe(200);
    expect(me.body).toMatchObject({ role: 'marketer', sections: ['stats'], creatable_roles: [] });
  });

  it('/me: rol, ruxsatlar, yarata oladigan rollar (roles.json dan)', async () => {
    const me = await h.as('manager').get('/admin/v1/me');
    expect(me.body.creatable_roles).toEqual(['marketer', 'operator']);
    expect(me.body.permissions).toContain('staff:write');
    const op = await h.as('op1').get('/admin/v1/me');
    expect(op.body).toMatchObject({ role: 'operator', sections: ['operator', 'complaints'], creatable_roles: [] });
  });

  it('ruxsat matritsasi: marketolog faqat stats, operator faqat operator, yuqori rollar hammasi', async () => {
    const endpoints = [
      ['stats', '/admin/v1/stats'], ['staff', '/admin/v1/staff'], ['operator', '/admin/v1/operator/queue'],
      ['orders', '/admin/v1/orders'], ['wallet', '/admin/v1/wallet/ledger'], ['tariffs', '/admin/v1/tariffs'],
      ['drivers', '/admin/v1/drivers'], ['passengers', '/admin/v1/passengers'],
    ] as const;
    const expected: Record<string, string[]> = {
      root: endpoints.map((e) => e[0]), owner: endpoints.map((e) => e[0]), manager: endpoints.map((e) => e[0]),
      marketer: ['stats'], op1: ['operator'],
    };
    for (const [who, allowed] of Object.entries(expected)) {
      for (const [section, url] of endpoints) {
        const r = await h.as(who).get(url);
        const want = allowed.includes(section) ? 200 : 403;
        if (r.status !== want) throw new Error(`${who} GET ${url}: ${r.status} (kutilgan ${want}) ${JSON.stringify(r.body)}`);
      }
    }
    // yozish amallari
    expect((await h.as('marketer').post('/admin/v1/tariffs').send({})).status).toBe(403); // validatsiyadan oldin ruxsat
    expect((await h.as('op1').post('/admin/v1/wallet/correction').send({})).status).toBe(403);
    expect((await h.as('op1').put(`/admin/v1/drivers/${[...h.mock.drivers.keys()][0]}/block`).send({ state_reason: 'x' })).status).toBe(403);
  });

  it('bloklangan/arxivlangan xodim token bilan ham kira olmaydi; rol o\'zgarishi darhol kuchga kiradi', async () => {
    const id = uuidv4();
    await h.ds.getRepository(Staff).insert({
      id, email: 'b@example.com', phone: '+998911111111', fullName: 'B', role: 'manager', state: 'active',
      createdBy: null, createdAt: new Date(), updatedAt: new Date(),
    } as never);
    const t = `Bearer ${makeToken(id)}`;
    expect((await h.http().get('/admin/v1/staff').set('Authorization', t)).status).toBe(200);
    await h.ds.getRepository(Staff).update({ id }, { role: 'marketer' });
    expect((await h.http().get('/admin/v1/staff').set('Authorization', t)).status).toBe(403); // token eski, DB yangi
    await h.ds.getRepository(Staff).update({ id }, { state: 'blocked' });
    const r = await h.http().get('/admin/v1/stats').set('Authorization', t);
    expect(r.status).toBe(403);
    expect(r.body.error.params.reason).toBe('staff_inactive');
    await h.ds.getRepository(Staff).update({ id }, { state: 'archived' });
    expect((await h.http().get('/admin/v1/stats').set('Authorization', t)).status).toBe(403);
  });

  it('noma\'lum sub: superadmin tokeni bir marta provision qilinadi, boshqa rol — 403', async () => {
    const id = uuidv4();
    h.mock.identityStaff.set(id, {
      id, email: 'Real.Root@Example.com', phone: '+998935550001', fullName: 'Haqiqiy Root', role: 'superadmin', active: true,
    });
    const before = (await h.audit('staff.provision')).length;
    const t = `Bearer ${makeToken(id, { role: 'superadmin' })}`;
    const me = await h.http().get('/admin/v1/me').set('Authorization', t);
    expect(me.status).toBe(200);
    // kontaktlar identity'dagi AYNAN shu akkauntniki (ADMIN_BOOTSTRAP_* emas)
    expect(me.body).toMatchObject({ id, role: 'superadmin', email: 'real.root@example.com', full_name: 'Haqiqiy Root' });
    const row = await h.ds.getRepository(Staff).findOneOrFail({ where: { id } });
    expect(row).toMatchObject({ email: 'real.root@example.com', phone: '+998935550001', fullName: 'Haqiqiy Root', state: 'active' });
    const prov = await h.audit('staff.provision');
    expect(prov.length).toBe(before + 1);
    expect(prov.find((a) => a.entity_id === id)?.after).toMatchObject({ email: 'real.root@example.com', phone: '+998935550001' });
    expect((await h.http().get('/admin/v1/me').set('Authorization', t)).status).toBe(200);
    expect((await h.audit('staff.provision')).length).toBe(before + 1);

    const op = `Bearer ${makeToken(uuidv4(), { role: 'owner' })}`;
    expect((await h.http().get('/admin/v1/me').set('Authorization', op)).status).toBe(403);
  });

  it('superadmin tokeni, lekin identity\'da yo\'q / nofaol / boshqa rol — 403, yozuv yaratilmaydi', async () => {
    const before = (await h.audit('staff.provision')).length;
    const absent = uuidv4();
    const inactive = uuidv4();
    const demoted = uuidv4();
    h.mock.identityStaff.set(inactive, {
      id: inactive, email: 'off@example.com', phone: '+998935550002', fullName: 'Off', role: 'superadmin', active: false,
    });
    h.mock.identityStaff.set(demoted, {
      id: demoted, email: 'mgr@example.com', phone: '+998935550003', fullName: 'Mgr', role: 'manager', active: true,
    });
    for (const id of [absent, inactive, demoted]) {
      const r = await h.http().get('/admin/v1/me').set('Authorization', `Bearer ${makeToken(id, { role: 'superadmin' })}`);
      expect(r.status).toBe(403);
      expect(r.body.error.params.reason).toBe('staff_inactive');
      expect(await h.ds.getRepository(Staff).findOne({ where: { id } })).toBeNull();
    }
    expect((await h.audit('staff.provision')).length).toBe(before);
  });

  it('identity javob bermasa — 502 IDENTITY_UNAVAILABLE, yozuv yaratilmaydi; keyingi so\'rovda yaratiladi', async () => {
    const id = uuidv4();
    h.mock.identityStaff.set(id, {
      id, email: 'later@example.com', phone: '+998935550004', fullName: 'Later', role: 'superadmin', active: true,
    });
    const port = h.app.get(IdentityStaffPort);
    const spy = jest.spyOn(port, 'get').mockRejectedValueOnce(Errors.identityUnavailable('timeout'));
    const t = `Bearer ${makeToken(id, { role: 'superadmin' })}`;
    const r = await h.http().get('/admin/v1/me').set('Authorization', t);
    expect(r.status).toBe(502);
    expect(r.body).toEqual({ error: { key: 'IDENTITY_UNAVAILABLE', params: { reason: 'timeout' } } });
    expect(await h.ds.getRepository(Staff).findOne({ where: { id } })).toBeNull();
    expect((await h.http().get('/admin/v1/me').set('Authorization', t)).body).toMatchObject({ id, email: 'later@example.com' });
    spy.mockRestore();
  });

  it('identity\'dagi email/telefon backoffice\'da boshqa xodimda band — kontaktsiz yaratiladi (kirish to\'silmaydi)', async () => {
    const id = uuidv4();
    const other = h.people.manager;
    const taken = await h.ds.getRepository(Staff).findOneOrFail({ where: { id: other.id } });
    h.mock.identityStaff.set(id, {
      id, email: taken.email, phone: taken.phone, fullName: 'Clash Root', role: 'superadmin', active: true,
    });
    const r = await h.http().get('/admin/v1/me').set('Authorization', `Bearer ${makeToken(id, { role: 'superadmin' })}`);
    expect(r.status).toBe(200);
    expect(await h.ds.getRepository(Staff).findOneOrFail({ where: { id } }))
      .toMatchObject({ email: null, phone: null, fullName: 'Clash Root', role: 'superadmin' });
    // boshqa xodimning yozuviga tegilmagan
    expect(await h.ds.getRepository(Staff).findOneOrFail({ where: { id: other.id } }))
      .toMatchObject({ email: taken.email, phone: taken.phone });
  });
});

describe('logout: bekor qilingan sessiya (Redis bl:sid:<sid>)', () => {
  let h: Harness;
  beforeAll(async () => { h = await createHarness(); });
  afterAll(async () => { await h.app.close(); });
  afterEach(() => { h.sessions.revoked.clear(); h.sessions.fail = false; });

  it('sid bekor qilingan token → 401 AUTH_TOKEN_EXPIRED; boshqa sessiya tokeni ishlaydi', async () => {
    const id = h.people.manager.id;
    const revoked = `Bearer ${makeToken(id, { sid: 'sid-logged-out' })}`;
    const alive = `Bearer ${makeToken(id, { sid: 'sid-alive' })}`;
    expect((await h.http().get('/admin/v1/me').set('Authorization', revoked)).status).toBe(200);
    h.sessions.revoked.add('sid-logged-out');
    const r = await h.http().get('/admin/v1/me').set('Authorization', revoked);
    expect(r.status).toBe(401);
    expect(r.body).toEqual({ error: { key: 'AUTH_TOKEN_EXPIRED', params: {} } });
    expect((await h.http().get('/admin/v1/staff').set('Authorization', revoked)).status).toBe(401);
    expect((await h.http().get('/admin/v1/me').set('Authorization', alive)).status).toBe(200);
  });

  it('bekor qilingan superadmin tokeni bootstrap provision qilmaydi', async () => {
    const id = uuidv4();
    h.sessions.revoked.add('sid-boot');
    const r = await h.http().get('/admin/v1/me').set('Authorization', `Bearer ${makeToken(id, { role: 'superadmin', sid: 'sid-boot' })}`);
    expect(r.status).toBe(401);
    expect(await h.ds.getRepository(Staff).findOne({ where: { id } })).toBeNull();
  });

  it('Redis ishlamasa — rad etiladi (fail closed, 500); /healthz Redis ga bog\'liq emas', async () => {
    h.sessions.fail = true;
    const r = await h.as('root').get('/admin/v1/me');
    expect(r.status).toBe(500);
    expect(r.body).toEqual({ error: { key: 'INTERNAL_ERROR', params: { reason: 'session_check_unavailable' } } });
    expect((await h.http().get('/healthz')).status).toBe(200);
  });

  it('RedisSessionRevocationPort: Redis ga ulanib bo\'lmasa ~timeout ichida xato beradi', async () => {
    const port = new RedisSessionRevocationPort('redis://127.0.0.1:1', 300);
    const t0 = Date.now();
    try {
      await expect(port.isRevoked('x')).rejects.toBeInstanceOf(Error);
      expect(Date.now() - t0).toBeLessThan(2500);
    } finally {
      port.close();
    }
  });
});
