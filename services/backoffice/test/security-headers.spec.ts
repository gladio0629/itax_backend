import { createHarness, Harness } from './support/harness';

const EXPECTED = {
  'content-security-policy': "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'",
  'x-frame-options': 'DENY',
  'x-content-type-options': 'nosniff',
  'strict-transport-security': 'max-age=31536000; includeSubDomains',
};

describe('xavfsizlik header\'lari (F-08)', () => {
  let h: Harness;
  beforeAll(async () => { h = await createHarness(); });
  afterAll(async () => { await h.app.close(); });

  const cases: [string, () => Promise<{ status: number; headers: Record<string, string> }>, number][] = [
    ['200 ochiq endpoint', () => h.http().get('/healthz'), 200],
    ['200 tokenli', () => h.as('root').get('/admin/v1/me'), 200],
    ['401 token yo\'q (guard)', () => h.http().get('/admin/v1/me'), 401],
    ['403 ruxsat yo\'q', () => h.as('marketer').get('/admin/v1/staff'), 403],
    ['404 noma\'lum yo\'l', () => h.as('root').get('/admin/v1/no-such-route'), 404],
    ['422 validatsiya', () => h.as('root').post('/admin/v1/staff').send({ role: 'x' }), 422],
  ];

  it.each(cases)('%s', async (_name, call, status) => {
    const r = await call();
    expect(r.status).toBe(status);
    expect(r.headers).toMatchObject(EXPECTED);
    expect(r.headers['x-powered-by']).toBeUndefined();
  });
});
