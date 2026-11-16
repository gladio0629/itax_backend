import { createServer, IncomingMessage, Server, ServerResponse } from 'http';
import { AddressInfo } from 'net';
import { v4 as uuidv4 } from 'uuid';
import { HttpDriverLookupPort, HttpIdentityStaffPort, HttpUserLookupPort } from '../src/ports/http-ports';
import { InternalHttpClient } from '../src/ports/internal-http';
import { loadConfig } from '../src/config/configuration';
import { createHarness, Harness, makeToken } from './support/harness';

const TOKEN = 'test-internal-token-0123456789';

/**
 * Identity'ning ichki API sini taqlaydigan HTTP server (haqiqiy tarmoq chaqiruvi).
 * Shartnoma: services/identity/internal/httpapi (POST /internal/identity/staff -> 201 {id,phone,role} | 409 {error:{key}}).
 */
class IdentityStub {
  server!: Server;
  url = '';
  calls: { method: string; url: string; token?: string; body: any }[] = [];
  taken = { emails: new Set<string>(), phones: new Set<string>() };
  mode: 'ok' | 'hang' | 'error500' | 'forbidden' = 'ok';
  users = new Map<string, { id: string; phone: string }>();
  missing = new Set<string>(); // identity'da yo'q xodimlar (PUT -> 404)
  blockedPhones = new Set<string>(); // identity'da boshqa xodimda band telefonlar (PUT -> 409)
  staff = new Map<string, Record<string, unknown>>(); // GET /internal/identity/staff/{id} javoblari
  staffGet: 'ok' | 'legacy405' | 'bare404' | 'garbage' = 'ok';

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

    if (this.mode === 'hang') return; // javob bermaydi
    if (this.mode === 'error500') return send(500, { error: { key: 'INTERNAL_ERROR', params: {} } });
    if (this.mode === 'forbidden' || token !== TOKEN) return send(403, { error: { key: 'FORBIDDEN', params: { reason: 'internal_only' } } });

    if (req.method === 'POST' && req.url === '/internal/identity/staff') {
      const b = JSON.parse(raw);
      if (b.email && this.taken.emails.has(b.email)) return send(409, { error: { key: 'STAFF_EMAIL_TAKEN', params: {} } });
      if (this.taken.phones.has(b.phone)) return send(409, { error: { key: 'STAFF_PHONE_TAKEN', params: {} } });
      if (b.email) this.taken.emails.add(b.email);
      this.taken.phones.add(b.phone);
      return send(201, { id: uuidv4(), phone: b.phone, role: b.role });
    }
    const sm = /^\/internal\/identity\/staff\/([^/]+)$/.exec(req.url!);
    if (req.method === 'GET' && sm) {
      // legacy405 — endpoint hali yo'q eski identity (Go mux: yo'l bor, metod yo'q)
      if (this.staffGet === 'legacy405') { res.writeHead(405, { 'Content-Type': 'text/plain' }); return void res.end('Method Not Allowed\n'); }
      if (this.staffGet === 'bare404') return send(404, { error: { key: 'NOT_FOUND', params: {} } });
      if (this.staffGet === 'garbage') return send(200, { id: sm[1], phone: 42 });
      const st = this.staff.get(sm[1]);
      return st ? send(200, st) : send(404, { error: { key: 'STAFF_NOT_FOUND', params: { staff_id: sm[1] } } });
    }
    if (req.method === 'PUT' && sm) {
      const b = JSON.parse(raw);
      if (this.missing.has(sm[1])) return send(404, { error: { key: 'STAFF_NOT_FOUND', params: { staff_id: sm[1] } } });
      if (b.phone && this.blockedPhones.has(b.phone)) return send(409, { error: { key: 'STAFF_PHONE_TAKEN', params: {} } });
      return send(200, { id: sm[1], phone: b.phone, email: b.email, role: b.role, name: b.name, active: b.active });
    }
    const m = /^\/internal\/(?:driver\/drivers|identity\/users)\/([^/]+)$/.exec(req.url!);
    if (req.method === 'GET' && m) {
      const u = this.users.get(m[1]);
      return u ? send(200, u) : send(404, { error: { key: 'NOT_FOUND', params: {} } });
    }
    send(404, { error: { key: 'NOT_FOUND', params: {} } });
  }
}

describe('Identity bilan HTTP sinxronlash', () => {
  const stub = new IdentityStub();
  beforeAll(() => stub.start());
  afterAll(() => stub.stop());
  beforeEach(() => {
    stub.calls = []; stub.taken.emails.clear(); stub.taken.phones.clear(); stub.mode = 'ok';
    stub.missing.clear(); stub.blockedPhones.clear(); stub.staff.clear(); stub.staffGet = 'ok';
  });

  const port = (timeoutMs = 3000, token = TOKEN) =>
    new HttpIdentityStaffPort(new InternalHttpClient(stub.url, token, timeoutMs));
  const input = { phone: '+998901234567', email: 'a@example.com', role: 'manager', name: 'Ali Valiyev' };

  describe('HttpIdentityStaffPort', () => {
    it('201 → identity bergan id qaytadi; X-Internal-Token va body to\'g\'ri', async () => {
      const r = await port().create(input);
      expect(r.id).toMatch(/^[0-9a-f-]{36}$/);
      expect(stub.calls).toHaveLength(1);
      expect(stub.calls[0]).toMatchObject({
        method: 'POST', url: '/internal/identity/staff', token: TOKEN,
        body: { phone: '+998901234567', email: 'a@example.com', role: 'manager', name: 'Ali Valiyev' },
      });
    });

    it('email null (operator) yuboriladi', async () => {
      await port().create({ ...input, email: null, role: 'operator' });
      expect(stub.calls[0].body.email).toBeNull();
    });

    it('409 → STAFF_PHONE_TAKEN / STAFF_EMAIL_TAKEN (409)', async () => {
      await port().create(input);
      await expect(port().create({ ...input, email: 'other@example.com' })).rejects.toMatchObject({ status: 409, key: 'STAFF_PHONE_TAKEN' });
      await expect(port().create({ ...input, phone: '+998901234568' })).rejects.toMatchObject({ status: 409, key: 'STAFF_EMAIL_TAKEN' });
    });

    it('javob bermasa (timeout) → 502, ulana olmasa → 502', async () => {
      stub.mode = 'hang';
      await expect(port(150).create(input)).rejects.toMatchObject({ status: 502, key: 'IDENTITY_UNAVAILABLE', params: { reason: 'timeout' } });
      const dead = new HttpIdentityStaffPort(new InternalHttpClient('http://127.0.0.1:1', TOKEN, 500));
      await expect(dead.create(input)).rejects.toMatchObject({ status: 502, params: { reason: 'unreachable' } });
    });

    it('5xx va noto\'g\'ri token (403) → 502', async () => {
      stub.mode = 'error500';
      await expect(port().create(input)).rejects.toMatchObject({ status: 502, params: { reason: 'http_500' } });
      stub.mode = 'ok';
      await expect(port(3000, 'boshqa-token-0123456789').create(input)).rejects.toMatchObject({ status: 502, params: { reason: 'http_403' } });
    });

    describe('get: GET /internal/identity/staff/{id}', () => {
      const id = '0193a000-0000-7000-8000-00000000abcd';
      const full = { id, phone: '+998935550010', email: 'root@izgo.uz', role: 'superadmin', name: 'Root', active: true };

      it('200 → IdentityStaff (name → fullName), X-Internal-Token yuboriladi', async () => {
        stub.staff.set(id, full);
        expect(await port().get(id)).toEqual({ id, phone: full.phone, email: full.email, fullName: 'Root', role: 'superadmin', active: true });
        expect(stub.calls[0]).toMatchObject({ method: 'GET', url: `/internal/identity/staff/${id}`, token: TOKEN });
        stub.staff.set(id, { ...full, email: null, role: 'operator', active: false });
        expect(await port().get(id)).toMatchObject({ email: null, role: 'operator', active: false });
      });

      it('404 STAFF_NOT_FOUND → null', async () => {
        expect(await port().get(id)).toBeNull();
      });

      it('kalitsiz 404, eski identity (405), noto\'g\'ri shakl, boshqa id → 502 (null emas)', async () => {
        stub.staffGet = 'bare404';
        await expect(port().get(id)).rejects.toMatchObject({ status: 502, key: 'IDENTITY_UNAVAILABLE', params: { reason: 'http_404' } });
        stub.staffGet = 'legacy405';
        await expect(port().get(id)).rejects.toMatchObject({ status: 502, params: { reason: 'http_405' } });
        stub.staffGet = 'garbage';
        await expect(port().get(id)).rejects.toMatchObject({ status: 502, params: { reason: 'invalid_response' } });
        stub.staffGet = 'ok';
        stub.staff.set(id, { ...full, id: '0193a000-0000-7000-8000-00000000ffff' });
        await expect(port().get(id)).rejects.toMatchObject({ status: 502, params: { reason: 'invalid_response' } });
      });

      it('5xx, noto\'g\'ri token, timeout → 502', async () => {
        stub.mode = 'error500';
        await expect(port().get(id)).rejects.toMatchObject({ status: 502, params: { reason: 'http_500' } });
        stub.mode = 'ok';
        await expect(port(3000, 'boshqa-token-0123456789').get(id)).rejects.toMatchObject({ status: 502, params: { reason: 'http_403' } });
        stub.mode = 'hang';
        await expect(port(150).get(id)).rejects.toMatchObject({ status: 502, params: { reason: 'timeout' } });
      });
    });
  });

  describe('Superadmin provision (haqiqiy HTTP orqali)', () => {
    let h: Harness;
    beforeEach(async () => {
      h = await createHarness({ identity: { mode: 'http', baseUrl: stub.url, internalToken: TOKEN, timeoutMs: 2000 } });
    });
    afterEach(async () => { await h.app.close(); });

    it('birinchi kirish: email/telefon/ism identity\'dan olinadi', async () => {
      const id = uuidv4();
      stub.staff.set(id, { id, phone: '+998935550020', email: 'seed@izgo.test', role: 'superadmin', name: 'Seed Root', active: true });
      const r = await h.http().get('/admin/v1/me').set('Authorization', `Bearer ${makeToken(id, { role: 'superadmin' })}`);
      expect(r.status).toBe(200);
      expect(r.body).toMatchObject({ id, email: 'seed@izgo.test', full_name: 'Seed Root' });
      expect((await h.ds.query(`SELECT phone FROM staff WHERE id = '${id}'`))[0].phone).toBe('+998935550020');
    });

    it('identity\'da yo\'q → 403; identity eski (405) → 502; ikkalasida yozuv yaratilmaydi', async () => {
      const id = uuidv4();
      const t = `Bearer ${makeToken(id, { role: 'superadmin' })}`;
      expect((await h.http().get('/admin/v1/me').set('Authorization', t)).status).toBe(403);
      stub.staffGet = 'legacy405';
      const r = await h.http().get('/admin/v1/me').set('Authorization', t);
      expect(r.status).toBe(502);
      expect(r.body.error.key).toBe('IDENTITY_UNAVAILABLE');
      expect(await h.ds.query(`SELECT id FROM staff WHERE id = '${id}'`)).toHaveLength(0);
    });
  });

  describe('Backoffice POST /admin/v1/staff (haqiqiy HTTP orqali)', () => {
    let h: Harness;
    beforeEach(async () => {
      h = await createHarness({ identity: { mode: 'http', baseUrl: stub.url, internalToken: TOKEN, timeoutMs: 2000 } });
    });
    afterEach(async () => { await h.app.close(); });
    const body = { email: 'new@example.com', phone: '+998931112233', full_name: 'Yangi Xodim', role: 'marketer' };

    it('identity 201 → xodim yaratiladi va ID identity bergan ID bilan bir xil', async () => {
      const r = await h.as('owner').post('/admin/v1/staff').send(body);
      expect(r.status).toBe(201);
      expect(stub.calls).toHaveLength(1);
      expect(stub.calls[0].body).toEqual({ phone: body.phone, email: body.email, role: 'marketer', name: body.full_name });
      const row = (await h.ds.query(`SELECT id FROM staff WHERE email = 'new@example.com'`))[0];
      expect(row.id).toBe(r.body.id);
      expect((await h.audit('staff.create'))[0].entity_id).toBe(r.body.id);
    });

    it('emailsiz operator: identity ga email: null boradi', async () => {
      const r = await h.as('owner').post('/admin/v1/staff').send({ phone: '+998931112244', full_name: 'Op', role: 'operator' });
      expect(r.status).toBe(201);
      expect(stub.calls[0].body.email).toBeNull();
    });

    it('identity 409 → backoffice ham 409, xodim yaratilmaydi', async () => {
      stub.taken.phones.add(body.phone); // identity'da bor (backoffice bazasida yo'q)
      const r = await h.as('owner').post('/admin/v1/staff').send(body);
      expect(r.status).toBe(409);
      expect(r.body).toEqual({ error: { key: 'STAFF_PHONE_TAKEN', params: {} } });
      expect((await h.ds.query(`SELECT count(*)::int AS n FROM staff WHERE email = 'new@example.com'`))[0].n).toBe(0);
      expect(await h.audit('staff.create')).toHaveLength(0);

      stub.taken.phones.clear();
      stub.taken.emails.add(body.email);
      expect((await h.as('owner').post('/admin/v1/staff').send(body)).body.error.key).toBe('STAFF_EMAIL_TAKEN');
    });

    it('identity javob bermasa → 502 IDENTITY_UNAVAILABLE, xodim yaratilmaydi', async () => {
      stub.mode = 'hang';
      const r = await h.as('owner').post('/admin/v1/staff').send(body);
      expect(r.status).toBe(502);
      expect(r.body).toEqual({ error: { key: 'IDENTITY_UNAVAILABLE', params: { reason: 'timeout' } } });
      expect((await h.ds.query(`SELECT count(*)::int AS n FROM staff WHERE email = 'new@example.com'`))[0].n).toBe(0);
      expect(await h.audit()).toHaveLength(0);
    });

    it('o\'z bazamizda band bo\'lsa identity ga umuman murojaat qilinmaydi', async () => {
      await h.as('owner').post('/admin/v1/staff').send(body).expect(201);
      stub.calls = [];
      const r = await h.as('owner').post('/admin/v1/staff').send(body);
      expect(r.status).toBe(409);
      expect(stub.calls).toHaveLength(0);
    });

    it('tahrirlash va arxivlash identity ga PUT /internal/identity/staff/{id} bilan yuboriladi', async () => {
      const id = (await h.as('owner').post('/admin/v1/staff').send(body)).body.id;
      stub.calls = [];
      await h.as('owner').put(`/admin/v1/staff/${id}`).send({ full_name: 'Boshqa Ism' }).expect(200);
      await h.as('owner').delete(`/admin/v1/staff/${id}`).expect(204);
      expect(stub.calls).toHaveLength(2);
      expect(stub.calls[0]).toMatchObject({
        method: 'PUT', url: `/internal/identity/staff/${id}`, token: TOKEN,
        body: { phone: body.phone, email: body.email, role: body.role, name: 'Boshqa Ism', active: true },
      });
      expect(stub.calls[1]).toMatchObject({ method: 'PUT', body: { active: false } });
    });

    it('identity rad etsa (409 / 404 / javobsiz) — xato qaytadi, audit yozilmaydi', async () => {
      // Audit identity chaqiruvidan KEYIN, o'sha tranzaksiyada yoziladi — xatoda umuman yozilmaydi.
      // (pg-mem ROLLBACK ni qo'llamaydi, shuning uchun xodim qatori bu yerda tekshirilmaydi —
      // haqiqiy PostgreSQL da tranzaksiya butunlay bekor bo'ladi; qarang: data.spec.ts.)
      const id = (await h.as('owner').post('/admin/v1/staff').send(body)).body.id;

      stub.blockedPhones.add('+998901119999');
      const r1 = await h.as('owner').put(`/admin/v1/staff/${id}`).send({ phone: '+998901119999', full_name: 'X' });
      expect(r1.status).toBe(409);
      expect(r1.body.error.key).toBe('STAFF_PHONE_TAKEN');

      stub.missing.add(id);
      const r2 = await h.as('owner').put(`/admin/v1/staff/${id}`).send({ full_name: 'Y' });
      expect(r2.status).toBe(502);
      expect(r2.body.error).toMatchObject({ key: 'IDENTITY_UNAVAILABLE', params: { reason: 'staff_not_in_identity' } });
      stub.missing.clear();

      stub.mode = 'error500';
      expect((await h.as('owner').put(`/admin/v1/staff/${id}`).send({ full_name: 'Z' })).status).toBe(502);
      expect((await h.as('owner').delete(`/admin/v1/staff/${id}`)).status).toBe(502);
      stub.mode = 'ok';

      expect(await h.audit('staff.update')).toHaveLength(0);
      expect(await h.audit('staff.archive')).toHaveLength(0);
    });
  });

  describe('Driver / User lookup (interfeys tayyor, default mock)', () => {
    const id = uuidv4();
    beforeEach(() => stub.users.set(id, { id, phone: '+998901110001' }));

    it('GET /internal/driver/drivers/{id} va /internal/identity/users/{id}', async () => {
      const c = () => new InternalHttpClient(stub.url, TOKEN, 500);
      expect(await new HttpDriverLookupPort(c()).getDriver(id)).toEqual({ id, phone: '+998901110001' });
      expect(await new HttpUserLookupPort(c()).getUser(id)).toEqual({ id, phone: '+998901110001' });
      expect(await new HttpDriverLookupPort(c()).getDriver(uuidv4())).toBeNull();
      expect(stub.calls.map((x) => x.url)).toEqual([
        `/internal/driver/drivers/${id}`, `/internal/identity/users/${id}`, expect.stringMatching(/^\/internal\/driver\/drivers\//),
      ]);
      expect(stub.calls.every((x) => x.token === TOKEN)).toBe(true);
      stub.mode = 'error500';
      await expect(new HttpUserLookupPort(c()).getUser(id)).rejects.toMatchObject({ status: 502 });
    });

    it('default sozlama — mock (INTERNAL_LOOKUP_MODE berilmagan)', () => {
      const cfg = loadConfig({
        DB_HOST: 'h', DB_USER: 'u', DB_PASSWORD: 'p', DB_NAME: 'd', JWT_PUBLIC_KEY_PEM: 'x', INTERNAL_TOKENS: TOKEN,
      } as never);
      expect(cfg.lookups.mode).toBe('mock');
      expect(cfg.identity).toMatchObject({ mode: 'http', baseUrl: 'http://identity:8010', internalToken: TOKEN });
    });
  });

  describe('konfiguratsiya', () => {
    const base = { DB_HOST: 'h', DB_USER: 'u', DB_PASSWORD: 'p', DB_NAME: 'd', JWT_PUBLIC_KEY_PEM: 'x' };
    it('http rejimida INTERNAL_TOKENS majburiy (kamida 16 belgi)', () => {
      expect(() => loadConfig(base as never)).toThrow(/INTERNAL_TOKENS/);
      expect(() => loadConfig({ ...base, INTERNAL_TOKENS: 'short' } as never)).toThrow(/INTERNAL_TOKENS/);
      expect(() => loadConfig({ ...base, IDENTITY_STAFF_MODE: 'mock' } as never)).not.toThrow();
    });
    it('IDENTITY_INTERNAL_URL va vergulli INTERNAL_TOKENS (birinchisi yuboriladi)', () => {
      const cfg = loadConfig({ ...base, INTERNAL_TOKENS: `${TOKEN},old-token-0123456789`, IDENTITY_INTERNAL_URL: 'http://x:1/' } as never);
      expect(cfg.identity.baseUrl).toBe('http://x:1');
      expect(cfg.identity.internalToken).toBe(TOKEN);
    });
  });
});
