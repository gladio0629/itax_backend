import { v4 as uuidv4 } from 'uuid';
import { InboundEvents } from '../src/events/inbound-events';
import { createHarness, Harness } from './support/harness';

const loginEvent = (staffId: string, over: Record<string, unknown> = {}, payload: Record<string, unknown> = {}) => ({
  event: 'staff.logged_in', version: 1, event_id: uuidv4(), occurred_at: '2026-11-16T05:00:01.000Z', trace_id: 'req-1',
  payload: {
    staff_id: staffId, role: 'manager', session_id: 'sid-login-1', method: 'email', logged_in_at: '2026-11-16T05:00:00.000Z',
    ...payload,
  },
  ...over,
});

const logoutEvent = (staffId: string, payload: Record<string, unknown> = {}) => ({
  event: 'staff.logged_out', version: 1, event_id: uuidv4(), occurred_at: '2026-11-16T06:00:01.000Z',
  payload: { staff_id: staffId, role: 'manager', session_id: 'sid-login-1', logged_out_at: '2026-11-16T06:00:00.000Z', ...payload },
});

describe('login/logout auditi (F-07, identity hodisalari)', () => {
  let h: Harness;
  let inbound: InboundEvents;
  beforeAll(async () => {
    h = await createHarness();
    inbound = h.app.get(InboundEvents);
  });
  afterAll(async () => { await h.app.close(); });

  it('staff.logged_in -> auth.login: kim, qachon, qaysi sessiya; PII yo\'q', async () => {
    const ev = loginEvent(h.people.manager.id);
    await inbound.onStaffLoggedIn(ev);

    const rows = await h.ds.query(`SELECT * FROM audit_log WHERE id = $1`, [ev.event_id]);
    expect(rows).toHaveLength(1);
    const r = rows[0];
    expect(r).toMatchObject({
      actor_id: h.people.manager.id, actor_role: 'manager', action: 'auth.login',
      entity_type: 'staff_session', entity_id: 'sid-login-1', before: null,
      after: { session_id: 'sid-login-1', method: 'email' }, request_id: 'req-1',
    });
    expect(new Date(r.created_at).toISOString()).toBe('2026-11-16T05:00:00.000Z');
    expect(JSON.stringify(r)).not.toMatch(/@example\.com|\+998/);
  });

  it('qayta yetkazilgan hodisa (bir xil event_id) ikkinchi yozuv yaratmaydi', async () => {
    const ev = loginEvent(h.people.manager2.id, {}, { session_id: 'sid-dup' });
    await inbound.onStaffLoggedIn(ev);
    await inbound.onStaffLoggedIn(ev);
    await inbound.onStaffLoggedIn({ ...ev });
    const rows = await h.ds.query(`SELECT id FROM audit_log WHERE entity_id = 'sid-dup'`);
    expect(rows).toHaveLength(1);
  });

  it('staff.logged_out -> auth.logout', async () => {
    const ev = logoutEvent(h.people.manager.id);
    await inbound.onStaffLoggedOut(ev);
    const [r] = await h.ds.query(`SELECT * FROM audit_log WHERE id = $1`, [ev.event_id]);
    expect(r).toMatchObject({
      actor_id: h.people.manager.id, action: 'auth.logout', entity_type: 'staff_session', entity_id: 'sid-login-1',
      after: { session_id: 'sid-login-1' },
    });
    expect(new Date(r.created_at).toISOString()).toBe('2026-11-16T06:00:00.000Z');
  });

  it('rol backoffice DB dan olinadi; xodim hali yo\'q bo\'lsa — hodisadagi rol', async () => {
    const ev1 = loginEvent(h.people.op1.id, {}, { role: 'superadmin', session_id: 'sid-role-db', method: 'sms' });
    await inbound.onStaffLoggedIn(ev1);
    const unknown = uuidv4();
    const ev2 = loginEvent(unknown, {}, { role: 'superadmin', session_id: 'sid-role-ev' });
    await inbound.onStaffLoggedIn(ev2);
    const [a] = await h.ds.query(`SELECT actor_role FROM audit_log WHERE id = $1`, [ev1.event_id]);
    const [b] = await h.ds.query(`SELECT actor_id, actor_role FROM audit_log WHERE id = $1`, [ev2.event_id]);
    expect(a.actor_role).toBe('operator');
    expect(b).toEqual({ actor_id: unknown, actor_role: 'superadmin' });
  });

  it.each([
    ['event nomi boshqa', { event: 'user.created' }, {}],
    ['event_id UUID emas', { event_id: 'x' }, {}],
    ['staff_id yo\'q', {}, { staff_id: undefined }],
    ['rol staff emas', {}, { role: 'driver' }],
    ['method noma\'lum', {}, { method: 'password' }],
    ['session_id bo\'sh', {}, { session_id: '' }],
    ['session_id juda uzun', {}, { session_id: 'x'.repeat(65) }],
    ['vaqt noto\'g\'ri', {}, { logged_in_at: 'kecha' }],
  ])('noto\'g\'ri format (%s) -> SyntaxError, yozuv yo\'q', async (_n, over, payload) => {
    const before = (await h.audit()).length;
    await expect(inbound.onStaffLoggedIn(loginEvent(h.people.owner.id, over, payload))).rejects.toBeInstanceOf(SyntaxError);
    expect((await h.audit()).length).toBe(before);
  });

  it('audit API: auth.login yozuvlari audit:read ruxsati bilan ko\'rinadi', async () => {
    const r = await h.as('owner').get('/admin/v1/audit?action=auth.login');
    expect(r.status).toBe(200);
    expect(r.body.data.length).toBeGreaterThanOrEqual(1);
    expect(r.body.data[0]).toMatchObject({ action: 'auth.login', entity_type: 'staff_session' });
  });
});
