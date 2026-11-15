import { v4 as uuidv4 } from 'uuid';
import { Staff } from '../src/entities/staff.entity';
import { createHarness, Harness } from './support/harness';

const P1 = '00000000-0000-4000-8000-000000000301';
const D1 = '00000000-0000-4000-8000-000000000101';
const O1 = '00000000-0000-4000-8000-000000000401';

describe('shikoyatlar: /admin/v1/complaints', () => {
  let h: Harness;
  beforeEach(async () => { h = await createHarness(); });
  afterEach(async () => { await h.app.close(); });

  const body = (extra: object = {}) => ({
    order_id: O1, subject_type: 'driver', subject_id: D1, category: 'driver_behavior', description: '  Haydovchi qo\'pol gapirdi  ', ...extra,
  });
  const create = async (who = 'op1', extra: object = {}) => {
    const r = await h.as(who).post('/admin/v1/complaints').send(body(extra));
    if (r.status !== 201) throw new Error(`${r.status} ${JSON.stringify(r.body)}`);
    return r.body;
  };

  it('yaratish → 201 open, created_by = xodim; audit', async () => {
    const r = await h.as('op1').post('/admin/v1/complaints').send(body());
    expect(r.status).toBe(201);
    expect(r.body).toEqual({
      id: expect.any(String), order_id: O1, subject_type: 'driver', subject_id: D1, category: 'driver_behavior',
      description: 'Haydovchi qo\'pol gapirdi', status: 'open', assigned_to: null, resolution: null,
      created_by: h.people.op1.id, created_at: expect.any(String), updated_at: expect.any(String),
    });
    const a = await h.audit('complaint.create');
    expect(a).toHaveLength(1);
    expect(a[0]).toMatchObject({ actor_id: h.people.op1.id, entity_id: r.body.id, before: null, after: { status: 'open', subject_id: D1 } });
    // order_id ixtiyoriy
    expect((await h.as('op1').post('/admin/v1/complaints').send(body({ order_id: undefined, subject_type: 'passenger', subject_id: P1, category: 'payment' }))).body.order_id).toBeNull();
  });

  it('yaratish validatsiyasi: 422 (noma\'lum maydon, enum, uuid, tavsif uzunligi)', async () => {
    const bad = async (extra: object) => (await h.as('op1').post('/admin/v1/complaints').send(body(extra))).body.error;
    expect(await bad({ subject_type: 'car' })).toMatchObject({ key: 'VALIDATION_ERROR', params: { fields: { subject_type: 'isIn' } } });
    expect((await bad({ category: 'x' })).params.fields).toHaveProperty('category');
    expect((await bad({ subject_id: 'abc' })).params.fields).toHaveProperty('subject_id');
    expect((await bad({ order_id: 'abc' })).params.fields).toHaveProperty('order_id');
    expect((await bad({ description: '   ' })).params.fields).toHaveProperty('description');
    expect((await bad({ description: 'x'.repeat(2001) })).params.fields).toHaveProperty('description');
    expect((await bad({ status: 'resolved' })).params.fields).toHaveProperty('status'); // ortiqcha maydon
    expect((await h.as('op1').post('/admin/v1/complaints').send(body({ description: 'x'.repeat(2000) }))).status).toBe(201);
    expect(await h.audit('complaint.create')).toHaveLength(1);
  });

  it('ro\'yxat: yangilari birinchi, filtrlar, sahifalash; batafsil + izohlar (eskisi birinchi); 404', async () => {
    const a = await create('op1');
    const b = await create('op2', { subject_type: 'passenger', subject_id: P1, category: 'lost_item' });
    const c = await create('manager');
    await h.as('manager').post(`/admin/v1/complaints/${a.id}/assign`).send({ staff_id: h.people.op2.id }).expect(200);

    const all = await h.as('op1').get('/admin/v1/complaints');
    expect(all.status).toBe(200);
    expect(all.body.data.map((x: any) => x.id)).toEqual([c.id, b.id, a.id]);
    expect((await h.as('op1').get('/admin/v1/complaints?status=in_progress')).body.data.map((x: any) => x.id)).toEqual([a.id]);
    expect((await h.as('op1').get(`/admin/v1/complaints?assigned_to=${h.people.op2.id}`)).body.data.map((x: any) => x.id)).toEqual([a.id]);
    expect((await h.as('op1').get('/admin/v1/complaints?subject_type=passenger')).body.data.map((x: any) => x.id)).toEqual([b.id]);
    expect((await h.as('op1').get('/admin/v1/complaints?status=open&subject_type=driver')).body.data.map((x: any) => x.id)).toEqual([c.id]);
    const p1 = await h.as('op1').get('/admin/v1/complaints?limit=2');
    expect(p1.body.data).toHaveLength(2);
    const p2 = await h.as('op1').get(`/admin/v1/complaints?limit=2&cursor=${p1.body.cursor}`);
    expect(p2.body).toEqual({ data: [expect.objectContaining({ id: a.id })], cursor: null });
    expect((await h.as('op1').get('/admin/v1/complaints?status=closed')).status).toBe(422);
    expect((await h.as('op1').get('/admin/v1/complaints?assigned_to=x')).status).toBe(422);

    const n1 = await h.as('op1').post(`/admin/v1/complaints/${a.id}/notes`).send({ text: 'Haydovchiga qo\'ng\'iroq qilindi' });
    expect(n1.status).toBe(201);
    expect(n1.body).toEqual({ id: expect.any(String), complaint_id: a.id, author_id: h.people.op1.id, text: 'Haydovchiga qo\'ng\'iroq qilindi', created_at: expect.any(String) });
    const n2 = (await h.as('op2').post(`/admin/v1/complaints/${a.id}/notes`).send({ text: 'Ikkinchi izoh' })).body;

    const one = await h.as('op2').get(`/admin/v1/complaints/${a.id}`);
    expect(one.status).toBe(200);
    expect(one.body).toMatchObject({ id: a.id, status: 'in_progress', assigned_to: h.people.op2.id });
    expect(one.body.notes.map((n: any) => n.id)).toEqual([n1.body.id, n2.id]);
    expect((await h.as('op2').get(`/admin/v1/complaints/${b.id}`)).body.notes).toEqual([]);

    const missing = uuidv4();
    const nf = await h.as('op1').get(`/admin/v1/complaints/${missing}`);
    expect(nf.status).toBe(404);
    expect(nf.body).toEqual({ error: { key: 'NOT_FOUND', params: { id: missing } } });
    expect((await h.as('op1').post(`/admin/v1/complaints/${missing}/notes`).send({ text: 'x' })).status).toBe(404);
    expect((await h.as('op1').get('/admin/v1/complaints/abc')).status).toBe(422);
    expect(await h.audit('complaint.note.add')).toHaveLength(2);
  });

  it('izoh validatsiyasi: 1..1000, ortiqcha maydon yo\'q', async () => {
    const c = await create();
    const url = `/admin/v1/complaints/${c.id}/notes`;
    expect((await h.as('op1').post(url).send({ text: '' })).status).toBe(422);
    expect((await h.as('op1').post(url).send({ text: 'x'.repeat(1001) })).status).toBe(422);
    expect((await h.as('op1').post(url).send({ text: 'ok', author_id: h.people.root.id })).status).toBe(422);
    expect((await h.as('op1').post(url).send({ text: 'x'.repeat(1000) })).status).toBe(201);
  });

  it('tayinlash: faqat faol xodim (aks holda 422); open → in_progress; qayta tayinlash; yakunlangan — 403', async () => {
    const c = await create();
    const url = `/admin/v1/complaints/${c.id}/assign`;
    expect((await h.as('manager').post(url).send({ staff_id: uuidv4() })).body.error.params.fields).toEqual({ staff_id: 'not_found' });
    await h.ds.getRepository(Staff).update({ id: h.people.manager2.id }, { state: 'blocked' });
    expect((await h.as('manager').post(url).send({ staff_id: h.people.manager2.id })).body.error.params.fields).toEqual({ staff_id: 'inactive' });
    expect((await h.as('manager').post(url).send({ staff_id: 'abc' })).status).toBe(422);
    expect((await h.as('manager').post(url).send({})).status).toBe(422);

    const r = await h.as('manager').post(url).send({ staff_id: h.people.op1.id });
    expect(r.status).toBe(200);
    expect(r.body).toMatchObject({ status: 'in_progress', assigned_to: h.people.op1.id });
    const r2 = await h.as('manager').post(url).send({ staff_id: h.people.op2.id });
    expect(r2.body).toMatchObject({ status: 'in_progress', assigned_to: h.people.op2.id });
    const audit = await h.audit('complaint.assign');
    expect(audit).toHaveLength(2);
    expect(audit[0]).toMatchObject({ before: { status: 'open', assigned_to: null }, after: { status: 'in_progress', assigned_to: h.people.op1.id } });

    await h.as('op2').post(`/admin/v1/complaints/${c.id}/status`).send({ status: 'resolved', resolution: 'Hal qilindi' }).expect(200);
    const closed = await h.as('manager').post(url).send({ staff_id: h.people.op1.id });
    expect(closed.status).toBe(403);
    expect(closed.body).toEqual({ error: { key: 'FORBIDDEN', params: { reason: 'complaint_closed' } } });
    expect((await h.as('manager').post(`/admin/v1/complaints/${uuidv4()}/assign`).send({ staff_id: h.people.op1.id })).status).toBe(404);
  });

  it('holat o\'tishlari: open→in_progress|rejected, in_progress→resolved|rejected; yakuniy — 403 invalid_transition', async () => {
    const st = (id: string, b: object, who = 'op1') => h.as(who).post(`/admin/v1/complaints/${id}/status`).send(b);

    const a = await create();
    // open → resolved mumkin emas
    const bad = await st(a.id, { status: 'resolved', resolution: 'Hal qilindi' });
    expect(bad.status).toBe(403);
    expect(bad.body).toEqual({ error: { key: 'FORBIDDEN', params: { reason: 'invalid_transition' } } });
    // open → in_progress: mas'ul bo'lmasa — bajargan xodim
    const p = await st(a.id, { status: 'in_progress' });
    expect(p.status).toBe(200);
    expect(p.body).toMatchObject({ status: 'in_progress', assigned_to: h.people.op1.id, resolution: null });
    expect((await st(a.id, { status: 'in_progress' })).body.error.params.reason).toBe('invalid_transition');
    // resolution majburiy / uzunligi
    expect((await st(a.id, { status: 'resolved' })).body.error.params.fields).toEqual({ resolution: 'required' });
    expect((await st(a.id, { status: 'resolved', resolution: 'ab' })).status).toBe(422);
    expect((await st(a.id, { status: 'resolved', resolution: 'x'.repeat(1001) })).status).toBe(422);
    expect((await st(a.id, { status: 'open' })).status).toBe(422);
    const res = await st(a.id, { status: 'resolved', resolution: '  Haydovchi ogohlantirildi ' });
    expect(res.status).toBe(200);
    expect(res.body).toMatchObject({ status: 'resolved', resolution: 'Haydovchi ogohlantirildi' });
    for (const s of ['in_progress', 'rejected', 'resolved']) {
      const r = await st(a.id, { status: s, ...(s === 'in_progress' ? {} : { resolution: 'qayta urinish' }) });
      expect(r.status).toBe(403);
      expect(r.body.error.params.reason).toBe('invalid_transition');
    }

    // open → rejected (to'g'ridan-to'g'ri)
    const b = await create();
    expect((await st(b.id, { status: 'in_progress', resolution: 'kerak emas' })).body.error.params.fields).toEqual({ resolution: 'not_allowed' });
    const rj = await st(b.id, { status: 'rejected', resolution: 'Asossiz shikoyat' });
    expect(rj.body).toMatchObject({ status: 'rejected', resolution: 'Asossiz shikoyat', assigned_to: null });
    expect((await st(b.id, { status: 'in_progress' })).status).toBe(403);
    // in_progress → rejected
    const c = await create();
    await st(c.id, { status: 'in_progress' }, 'op2').expect(200);
    expect((await st(c.id, { status: 'rejected', resolution: 'Takroriy' })).body.status).toBe('rejected');

    expect((await h.audit()).map((x) => x.action).filter((x) => x !== 'complaint.create')).toEqual([
      'complaint.start', 'complaint.resolve', 'complaint.reject', 'complaint.start', 'complaint.reject',
    ]);
    const resolveAudit = (await h.audit('complaint.resolve'))[0];
    expect(resolveAudit).toMatchObject({ entity_id: a.id, before: { status: 'in_progress' }, after: { status: 'resolved', resolution: 'Haydovchi ogohlantirildi' } });
    // yakunlangan shikoyatga izoh qo'shish mumkin
    expect((await h.as('op1').post(`/admin/v1/complaints/${a.id}/notes`).send({ text: 'Keyingi kuzatuv' })).status).toBe(201);
  });

  it('ruxsat: marketolog ko\'ra ham olmaydi; operator va manager — o\'qish va yozish', async () => {
    const c = await create('op1');
    expect((await h.as('marketer').get('/admin/v1/complaints')).status).toBe(403);
    expect((await h.as('marketer').get(`/admin/v1/complaints/${c.id}`)).status).toBe(403);
    expect((await h.as('marketer').post('/admin/v1/complaints').send({})).status).toBe(403); // validatsiyadan oldin ruxsat
    expect((await h.as('marketer').post(`/admin/v1/complaints/${c.id}/notes`).send({ text: 'x' })).status).toBe(403);
    expect((await h.as('marketer').post(`/admin/v1/complaints/${c.id}/status`).send({ status: 'in_progress' })).status).toBe(403);
    expect((await h.as('marketer').post(`/admin/v1/complaints/${c.id}/assign`).send({ staff_id: h.people.op1.id })).status).toBe(403);
    for (const who of ['root', 'owner', 'manager', 'op2']) {
      expect((await h.as(who).get('/admin/v1/complaints')).status).toBe(200);
    }
    expect((await h.as('manager').post(`/admin/v1/complaints/${c.id}/notes`).send({ text: 'ok' })).status).toBe(201);
  });
});
