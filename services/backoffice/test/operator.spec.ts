import { v4 as uuidv4 } from 'uuid';
import { OperatorService, maskPhone } from '../src/operator/operator.service';
import { createHarness, Harness } from './support/harness';

describe('operator navbati', () => {
  let h: Harness;
  let svc: OperatorService;
  const D1 = '00000000-0000-4000-8000-000000000101'; // mock haydovchi: +998901110001
  beforeEach(async () => { h = await createHarness(); svc = h.app.get(OperatorService); });
  afterEach(async () => { await h.app.close(); });
  const enqueue = async (driverId = D1) => {
    const ev = uuidv4();
    await svc.enqueueDriverApplication(ev, driverId);
    return (await h.ds.query(`SELECT id FROM queue_items WHERE source_event_id = '${ev}'`))[0].id as string;
  };

  it('maskPhone: 998**___**67', () => {
    expect(maskPhone('+998901234567')).toBe('998**___**67');
    expect(maskPhone(null)).toBeNull();
  });

  it('driver.submitted navbatga tushadi, takroriy event_id e\'tiborsiz; telefon maskalangan', async () => {
    const ev = uuidv4();
    expect(await svc.enqueueDriverApplication(ev, D1)).toBe(true);
    expect(await svc.enqueueDriverApplication(ev, D1)).toBe(false);
    const q = await h.as('op1').get('/admin/v1/operator/queue');
    expect(q.status).toBe(200);
    expect(q.body.data).toHaveLength(1);
    expect(q.body.data[0]).toMatchObject({ kind: 'driver_application', subject_id: D1, status: 'waiting', subject_phone: '998**___**01', assigned_to: null });
    expect(q.body.cursor).toBeNull();
    expect((await h.audit('operator.enqueue'))[0].actor_id).toBeNull();
  });

  it('start: waiting → in_progress, to\'liq telefon faqat boshlagan operatorga', async () => {
    const id = await enqueue();
    const s = await h.as('op1').post(`/admin/v1/operator/queue/${id}/start`);
    expect(s.status).toBe(200);
    expect(s.body).toMatchObject({ status: 'in_progress', assigned_to: h.people.op1.id, subject_phone: '+998901110001' });
    // boshqa operator niqoblangan raqamni ko'radi, o'zining navbatida bu ish yo'q
    const other = await h.as('op2').get('/admin/v1/operator/queue?section=in_progress');
    expect(other.body.data).toHaveLength(0);
    const mine = await h.as('op1').get('/admin/v1/operator/queue?section=in_progress');
    expect(mine.body.data[0].subject_phone).toBe('+998901110001');
    // yuqori rol hammasini ko'radi, lekin raqam niqoblangan
    const senior = await h.as('manager').get('/admin/v1/operator/queue?section=in_progress');
    expect(senior.body.data[0]).toMatchObject({ id, subject_phone: '998**___**01' });
    // kutayotganlar ro'yxatidan chiqdi
    expect((await h.as('op2').get('/admin/v1/operator/queue')).body.data).toHaveLength(0);
    // qayta boshlab bo'lmaydi
    const again = await h.as('op2').post(`/admin/v1/operator/queue/${id}/start`);
    expect(again.status).toBe(403);
    expect(again.body.error.params.reason).toBe('not_waiting');
    expect((await h.audit('operator.start'))).toHaveLength(1);
  });

  it('ikki operator bir vaqtda boshlasa — faqat bittasi yutadi', async () => {
    const id = await enqueue();
    const [a, b] = await Promise.all([
      h.as('op1').post(`/admin/v1/operator/queue/${id}/start`),
      h.as('op2').post(`/admin/v1/operator/queue/${id}/start`),
    ]);
    expect([a.status, b.status].sort()).toEqual([200, 403]);
    expect((await h.audit('operator.start'))).toHaveLength(1);
  });

  it('complete: faqat in_progress va shu operatorga biriktirilgan; resolved → driver.approved', async () => {
    const id = await enqueue();
    const url = `/admin/v1/operator/queue/${id}/complete`;
    const early = await h.as('op1').post(url).send({ result: 'resolved' });
    expect(early.status).toBe(403);
    expect(early.body.error.params.reason).toBe('not_in_progress');

    await h.as('op1').post(`/admin/v1/operator/queue/${id}/start`).expect(200);
    const foreign = await h.as('op2').post(url).send({ result: 'resolved' });
    expect(foreign.status).toBe(403);
    expect(foreign.body.error.params.reason).toBe('not_assigned');
    expect((await h.as('manager').post(url).send({ result: 'resolved' })).status).toBe(403); // yuqori rol ham begona ishni yakunlay olmaydi

    expect((await h.as('op1').post(url).send({ result: 'resolved', comment: 'x'.repeat(1001) })).status).toBe(422);
    expect((await h.as('op1').post(url).send({ result: 'maybe' })).status).toBe(422);
    expect((await h.as('op1').post(url).send({})).status).toBe(422);

    const ok = await h.as('op1').post(url).send({ result: 'resolved', comment: 'Hujjatlar to\'g\'ri' });
    expect(ok.status).toBe(200);
    expect(ok.body).toMatchObject({ status: 'completed', result: 'resolved', comment: 'Hujjatlar to\'g\'ri' });
    expect(ok.body.finished_at).toEqual(expect.any(String));

    const again = await h.as('op1').post(url).send({ result: 'resolved' });
    expect(again.status).toBe(403);
    expect(again.body.error.params.reason).toBe('not_in_progress');

    // driver.approved: producer backoffice, approved_by = operator (outbox → NATS)
    await h.dispatcher.drain();
    expect(h.publisher.sent).toHaveLength(1);
    const ev = h.publisher.sent[0];
    expect(ev.subject).toBe('izgo.driver.approved.v1');
    expect(ev.payload).toMatchObject({ event: 'driver.approved', version: 1, event_id: ev.msgId });
    expect((ev.payload as any).payload).toMatchObject({ driver_id: D1, approved_by: h.people.op1.id });
    expect((await h.audit('operator.complete'))).toHaveLength(1);
  });

  it('unresolved va boshqa turdagi ish uchun hodisa chiqmaydi', async () => {
    const id = await enqueue();
    await h.as('op1').post(`/admin/v1/operator/queue/${id}/start`).expect(200);
    await h.as('op1').post(`/admin/v1/operator/queue/${id}/complete`).send({ result: 'unresolved' }).expect(200);
    await h.dispatcher.drain();
    expect(h.publisher.sent).toHaveLength(0);
  });

  it('reject: waiting → rejected, sabab majburiy; boshlangan ishni rad etib bo\'lmaydi', async () => {
    const id = await enqueue();
    const url = `/admin/v1/operator/queue/${id}/reject`;
    expect((await h.as('op1').post(url).send({})).status).toBe(422);
    expect((await h.as('op1').post(url).send({ reason: 'x' })).status).toBe(422);
    const r = await h.as('op1').post(url).send({ reason: 'Hujjat sifatsiz' });
    expect(r.status).toBe(200);
    expect(r.body).toMatchObject({ status: 'rejected', reject_reason: 'Hujjat sifatsiz', assigned_to: null });
    expect((await h.as('op1').post(url).send({ reason: 'Hujjat sifatsiz' })).status).toBe(403);

    const id2 = await enqueue();
    await h.as('op2').post(`/admin/v1/operator/queue/${id2}/start`).expect(200);
    const late = await h.as('op1').post(`/admin/v1/operator/queue/${id2}/reject`).send({ reason: 'kech qoldi' });
    expect(late.status).toBe(403);
    expect(late.body.error.params.reason).toBe('not_waiting');
    expect((await h.as('op1').post('/admin/v1/operator/queue/00000000-0000-4000-8000-00000000dead/start')).body.error.key).toBe('NOT_FOUND');
    expect((await h.as('op1').post('/admin/v1/operator/queue/abc/start')).status).toBe(422);
  });

  it('tarix: operator faqat o\'zinikini ko\'radi; natija va sana bo\'yicha filtr', async () => {
    const a = await enqueue(); const b = await enqueue(); const c = await enqueue();
    await h.as('op1').post(`/admin/v1/operator/queue/${a}/start`);
    await h.as('op1').post(`/admin/v1/operator/queue/${a}/complete`).send({ result: 'resolved' }).expect(200);
    await h.as('op1').post(`/admin/v1/operator/queue/${b}/start`);
    await h.as('op1').post(`/admin/v1/operator/queue/${b}/complete`).send({ result: 'unresolved' }).expect(200);
    await h.as('op2').post(`/admin/v1/operator/queue/${c}/reject`).send({ reason: 'noto\'g\'ri' }).expect(200);

    const mine = await h.as('op1').get('/admin/v1/operator/history');
    expect(mine.body.data.map((x: any) => x.id).sort()).toEqual([a, b].sort());
    expect((await h.as('op2').get('/admin/v1/operator/history')).body.data.map((x: any) => x.id)).toEqual([c]);
    expect((await h.as('op1').get('/admin/v1/operator/history?result=resolved')).body.data.map((x: any) => x.id)).toEqual([a]);
    expect((await h.as('op2').get('/admin/v1/operator/history?result=rejected')).body.data).toHaveLength(1);
    const today = new Date(Date.now() + 5 * 3600_000).toISOString().slice(0, 10);
    expect((await h.as('op1').get(`/admin/v1/operator/history?from_date=${today}&to_date=${today}`)).body.data).toHaveLength(2);
    expect((await h.as('op1').get('/admin/v1/operator/history?to_date=2020-01-01')).body.data).toHaveLength(0);
    expect((await h.as('op1').get('/admin/v1/operator/history?from_date=2020-02-31')).status).toBe(422);
    expect((await h.as('op1').get('/admin/v1/operator/history?from_date=2026-02-01&to_date=2026-01-01')).status).toBe(422);
    expect((await h.as('op1').get('/admin/v1/operator/history?result=bogus')).status).toBe(422);
  });

  it('yuqori rollar 3 qismni ko\'radi; operator faqat navbat va o\'zinikini', async () => {
    const a = await enqueue(); const b = await enqueue(); await enqueue();
    await h.as('op1').post(`/admin/v1/operator/queue/${a}/start`);
    await h.as('op2').post(`/admin/v1/operator/queue/${b}/start`);
    await h.as('op2').post(`/admin/v1/operator/queue/${b}/complete`).send({ result: 'resolved' });

    for (const who of ['root', 'owner', 'manager']) {
      expect((await h.as(who).get('/admin/v1/operator/queue')).body.data).toHaveLength(1);
      expect((await h.as(who).get('/admin/v1/operator/queue?section=in_progress')).body.data.map((x: any) => x.id)).toEqual([a]);
      expect((await h.as(who).get('/admin/v1/operator/queue?section=completed_today')).body.data.map((x: any) => x.id)).toEqual([b]);
    }
    const guard = await h.as('op1').get('/admin/v1/operator/queue?section=completed_today');
    expect(guard.status).toBe(403);
    expect((await h.as('op1').get('/admin/v1/operator/queue?section=bogus')).status).toBe(422);
  });

  it('navbat eng eskisidan boshlanadi va cursor bilan sahifalanadi', async () => {
    const ids: string[] = [];
    for (let i = 0; i < 3; i++) { ids.push(await enqueue()); await new Promise((r) => setTimeout(r, 5)); }
    const p1 = await h.as('op1').get('/admin/v1/operator/queue?limit=2');
    expect(p1.body.data.map((x: any) => x.id)).toEqual([ids[0], ids[1]]);
    const p2 = await h.as('op1').get(`/admin/v1/operator/queue?limit=2&cursor=${p1.body.cursor}`);
    expect(p2.body.data.map((x: any) => x.id)).toEqual([ids[2]]);
    expect(p2.body.cursor).toBeNull();
  });
});
