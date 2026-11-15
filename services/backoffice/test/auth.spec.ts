import { v4 as uuidv4 } from 'uuid';
import { Staff } from '../src/entities/staff.entity';
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
    expect(op.body).toMatchObject({ role: 'operator', sections: ['operator'], creatable_roles: [] });
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
    const t = `Bearer ${makeToken(id, { role: 'superadmin' })}`;
    const me = await h.http().get('/admin/v1/me').set('Authorization', t);
    expect(me.status).toBe(200);
    expect(me.body).toMatchObject({ id, role: 'superadmin', email: 'boot@example.com' });
    expect((await h.audit('staff.provision')).length).toBe(1);
    expect((await h.http().get('/admin/v1/me').set('Authorization', t)).status).toBe(200);
    expect((await h.audit('staff.provision')).length).toBe(1);

    const op = `Bearer ${makeToken(uuidv4(), { role: 'owner' })}`;
    expect((await h.http().get('/admin/v1/me').set('Authorization', op)).status).toBe(403);
  });
});
