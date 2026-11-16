import { INestApplication } from '@nestjs/common';
import request from 'supertest';
import { ApprovalService } from '../src/approval/approval.service';
import {
  createHarness, EXIF_MARK, exifPngChunk, forgedToken, Harness, makeJpeg, makePng, makeToken,
} from './support/helpers';

const USER_A = '11111111-1111-4111-8111-111111111111';
const USER_B = '22222222-2222-4222-8222-222222222222';
const bearer = (user = USER_A) => `Bearer ${makeToken({ sub: user })}`;

describe('Driver service (e2e)', () => {
  let h: Harness;
  let app: INestApplication;
  const http = () => request(app.getHttpServer());

  beforeEach(async () => {
    h = await createHarness();
    app = h.app;
  });
  afterEach(async () => { await app.close(); });

  // --------------------------------------------------------------- yordamchi
  async function uploadDoc(user: string, kind: string, body?: Buffer, contentType = 'image/jpeg') {
    const buf = body ?? makeJpeg(true);
    const res = await http().post('/api/v1/drivers/documents/upload-url').set('Authorization', bearer(user))
      .send({ kind, content_type: contentType, size_bytes: buf.length });
    expect(res.status).toBe(201);
    const url = new URL(res.body.upload_url);
    expect(res.body.method).toBe('PUT');
    expect(url.pathname).toBe(`/api/v1/drivers/documents/${res.body.document_id}/content`);
    const put = await http().put(url.pathname + url.search).set(res.body.headers).send(buf);
    expect(put.status).toBe(204);
    return res.body.document_id as string;
  }

  const step1 = { first_name: 'Ali', last_name: 'Valiyev', birth_date: '1990-05-17', city: 'Toshkent' };
  const step2 = { car: { brand: 'Chevrolet', model: 'Cobalt', year: 2020, color: 'Oq', plate_number: '01 a 123 bc', car_class: 'economy', seats: 4 } };

  async function completeAll(user = USER_A) {
    const lic = await uploadDoc(user, 'license_front');
    const selfie = await uploadDoc(user, 'selfie');
    const a = bearer(user);
    expect((await http().post('/api/v1/drivers/registration/step/1').set('Authorization', a).send(step1)).status).toBe(200);
    expect((await http().post('/api/v1/drivers/registration/step/2').set('Authorization', a).send(step2)).status).toBe(200);
    const s3 = await http().post('/api/v1/drivers/registration/step/3').set('Authorization', a)
      .send({ documents: [{ kind: 'license_front', document_id: lic }, { kind: 'selfie', document_id: selfie }] });
    expect(s3.status).toBe(200);
    return { lic, selfie, a };
  }

  // -------------------------------------------------------------------- auth
  describe('token tekshiruvi', () => {
    it('token yo\'q → 401 AUTH_TOKEN_EXPIRED', async () => {
      const res = await http().get('/api/v1/drivers/me');
      expect(res.status).toBe(401);
      expect(res.body).toEqual({ error: { key: 'AUTH_TOKEN_EXPIRED', params: {} } });
    });
    it.each([
      ['begona kalit', () => forgedToken()],
      ['muddati o\'tgan', () => makeToken({ exp: Math.floor(Date.now() / 1000) - 10 })],
      ['refresh token', () => makeToken({ typ: 'refresh' })],
    ])('%s → 401', async (_n, mk) => {
      const res = await http().get('/api/v1/drivers/me').set('Authorization', `Bearer ${mk()}`);
      expect(res.status).toBe(401);
      expect(res.body.error.key).toBe('AUTH_TOKEN_EXPIRED');
    });
    it('yo\'lovchi (aud=client) tokeni → 403 FORBIDDEN', async () => {
      const res = await http().get('/api/v1/drivers/me').set('Authorization', `Bearer ${makeToken({ aud: ['client'] })}`);
      expect(res.status).toBe(403);
      expect(res.body).toEqual({ error: { key: 'FORBIDDEN', params: {} } });
    });
  });

  // ----------------------------------------------------------- 4 qadam oqimi
  describe('ro\'yxatdan o\'tish', () => {
    it('to\'liq oqim: 4 qadam → driver.submitted → avto-tasdiq → active + driver.approved', async () => {
      const { lic, selfie, a } = await completeAll();

      let st = await http().get('/api/v1/drivers/me/status').set('Authorization', a);
      expect(st.body).toMatchObject({ state: 'draft', reason: null });

      const s4 = await http().post('/api/v1/drivers/registration/step/4').set('Authorization', a)
        .set('X-Request-ID', 'req-123').send({ accept_terms: true });
      expect(s4.status).toBe(200);
      expect(s4.body).toEqual({ current_step: 4, completed_steps: [1, 2, 3, 4], state: 'pending_review' });

      // outbox → NATS: to'liq envelope, {pattern,data} o'ramisiz
      expect(await h.dispatcher.drain()).toBe(1);
      expect(h.publisher.sent).toHaveLength(1);
      const sub = h.publisher.sent[0];
      expect(sub.subject).toBe('izgo.driver.submitted.v1');
      expect(sub.payload).toMatchObject({ event: 'driver.submitted', version: 1, trace_id: 'req-123', event_id: sub.msgId });
      expect((sub.payload as any).payload).toMatchObject({ user_id: USER_A, document_ids: [lic, selfie] });
      expect(typeof (sub.payload as any).payload.driver_id).toBe('string');

      // demo: tekshiruvdan o'tish
      const approval = app.get(ApprovalService);
      expect(await approval.approve((sub.payload as any).payload.driver_id)).toBe(true);
      expect(await approval.approve((sub.payload as any).payload.driver_id)).toBe(false); // ikkinchi marta — no-op
      await h.dispatcher.drain();
      const appr = h.publisher.sent[1];
      expect(appr.subject).toBe('izgo.driver.approved.v1');
      expect((appr.payload as any).payload).toMatchObject({ approved_by: '00000000-0000-0000-0000-000000000001' });
      expect(h.publisher.sent).toHaveLength(2);

      st = await http().get('/api/v1/drivers/me/status').set('Authorization', a);
      expect(st.body.state).toBe('active');
    });

    it('avto-tasdiq: submitted dan keyin kechikish o\'tgach active bo\'ladi', async () => {
      await app.close();
      h = await createHarness({ autoEnabled: true, delayMs: 50 });
      app = h.app;
      const { a } = await completeAll();
      await http().post('/api/v1/drivers/registration/step/4').set('Authorization', a).send({ accept_terms: true });
      await new Promise((r) => setTimeout(r, 250));
      const st = await http().get('/api/v1/drivers/me/status').set('Authorization', a);
      expect(st.body.state).toBe('active');
      await h.dispatcher.drain();
      expect(h.publisher.sent.map((e) => e.subject)).toEqual(['izgo.driver.submitted.v1', 'izgo.driver.approved.v1']);
    });

    it('istalgan qadamdan davom etish: har qadam alohida saqlanadi', async () => {
      const a = bearer();
      let r = await http().post('/api/v1/drivers/registration/step/2').set('Authorization', a).send(step2);
      expect(r.body).toEqual({ current_step: 1, completed_steps: [2], state: 'draft' });
      r = await http().post('/api/v1/drivers/registration/step/1').set('Authorization', a).send(step1);
      expect(r.body).toEqual({ current_step: 3, completed_steps: [1, 2], state: 'draft' });
      // qayta yuborish ustidan yozadi
      r = await http().post('/api/v1/drivers/registration/step/1').set('Authorization', a).send({ ...step1, city: 'Samarqand' });
      expect(r.status).toBe(200);
      const me = await http().get('/api/v1/drivers/me').set('Authorization', a);
      expect(me.body).toMatchObject({ first_name: 'Ali', car: { plate_number: '01A123BC', brand: 'Chevrolet' }, state: 'draft' });
    });

    it('1–3 tugamasdan 4-qadam → 422, hodisa chiqmaydi', async () => {
      const a = bearer();
      await http().post('/api/v1/drivers/registration/step/1').set('Authorization', a).send(step1);
      const r = await http().post('/api/v1/drivers/registration/step/4').set('Authorization', a).send({ accept_terms: true });
      expect(r.status).toBe(422);
      expect(r.body).toEqual({ error: { key: 'VALIDATION_ERROR', params: { fields: { step_2: 'incomplete', step_3: 'incomplete' } } } });
      expect(h.store.outbox).toHaveLength(0);
    });

    it('4-qadamni takrorlash hodisani ikkilantirmaydi; yuborilgach 1–3 yopiq (409)', async () => {
      const { a } = await completeAll();
      const send = () => http().post('/api/v1/drivers/registration/step/4').set('Authorization', a).send({ accept_terms: true });
      const [r1, r2] = await Promise.all([send(), send()]);
      expect([r1.status, r2.status]).toEqual([200, 200]);
      expect(h.store.outbox.filter((o) => o.subject === 'izgo.driver.submitted.v1')).toHaveLength(1);

      const late = await http().post('/api/v1/drivers/registration/step/1').set('Authorization', a).send(step1);
      expect(late.status).toBe(409);
      expect(late.body.error.key).toBe('REGISTRATION_ALREADY_SUBMITTED');
    });

    it('validatsiya: noto\'g\'ri qadam, ortiqcha maydon, noto\'g\'ri sana, yosh', async () => {
      const a = bearer();
      const post = (s: string | number, body: unknown) => http().post(`/api/v1/drivers/registration/step/${s}`).set('Authorization', a).send(body as object);
      expect((await post(5, {})).status).toBe(422);
      expect((await post('abc', {})).status).toBe(422);
      const extra = await post(1, { ...step1, state: 'active' });
      expect(extra.status).toBe(422);
      expect(extra.body.error.params.fields.state).toBeDefined();
      expect((await post(1, { ...step1, birth_date: '1990-02-31' })).body.error.params.fields).toEqual({ birth_date: 'invalid_date' });
      expect((await post(1, { ...step1, birth_date: '2015-01-01' })).body.error.params.fields).toEqual({ birth_date: 'too_young' });
      expect((await post(2, { car: { ...step2.car, year: 1980 } })).body.error.params.fields).toEqual({ 'car.year': 'min' });
      expect((await post(4, { accept_terms: false })).status).toBe(422);
      // 1-qadam body'si 2-qadamga mos kelmaydi
      expect((await post(2, step1)).status).toBe(422);
      // F-09: har qadamda faqat o'sha qadam maydonlari (ichma-ich obyektlarda ham)
      const extraFields: [number, object, string][] = [
        [1, { ...step1, phone: '+998900000000' }, 'phone'],
        [1, { ...step1, role: 'admin' }, 'role'],
        [2, { ...step2, state: 'active' }, 'state'],
        [2, { car: { ...step2.car, owner_id: USER_B } }, 'car.owner_id'],
        [3, { documents: [], status: 'approved' }, 'status'],
        [3, { documents: [{ kind: 'selfie', document_id: USER_B, driver_id: USER_B }] }, 'documents.0.driver_id'],
        [4, { accept_terms: true, state: 'active' }, 'state'],
      ];
      for (const [s, body, field] of extraFields) {
        const r = await post(s, body);
        expect(r.status).toBe(422);
        expect(r.body.error.key).toBe('VALIDATION_ERROR');
        expect(r.body.error.params.fields[field]).toBe('whitelistValidation');
      }
      expect((await http().get('/api/v1/drivers/me').set('Authorization', a)).body.state).toBe('draft');
    });
  });

  // ----------------------------------------------------------------- hujjatlar
  describe('hujjat yuklash', () => {
    it('EXIF tozalanadi (JPEG va PNG), saqlangan fayl metadatasiz', async () => {
      const id = await uploadDoc(USER_A, 'license_front');
      const doc = h.store.docs.get(id)!;
      expect(doc.status).toBe('uploaded');
      const stored = h.storage.files.get(doc.storageKey!)!;
      expect(stored.includes(EXIF_MARK)).toBe(false);
      expect(stored.length).toBeLessThan(doc.sizeBytes); // metadata tushib ketgani uchun

      const png = makePng([exifPngChunk()]);
      const pid = await uploadDoc(USER_A, 'selfie', png, 'image/png');
      expect(h.storage.files.get(h.store.docs.get(pid)!.storageKey!)!.includes(EXIF_MARK)).toBe(false);
    });

    it('faqat JPEG/PNG va ≤10 MB: PDF, GIF, katta hajm → 422', async () => {
      const call = (b: object) => http().post('/api/v1/drivers/documents/upload-url').set('Authorization', bearer()).send(b);
      const pdf = await call({ kind: 'passport', content_type: 'application/pdf', size_bytes: 100 });
      expect(pdf.status).toBe(422);
      expect(pdf.body.error.params.fields.content_type).toBe('isIn');
      expect((await call({ kind: 'passport', content_type: 'image/gif', size_bytes: 100 })).status).toBe(422);
      const big = await call({ kind: 'passport', content_type: 'image/jpeg', size_bytes: 10 * 1024 * 1024 + 1 });
      expect(big.status).toBe(422);
      expect(big.body.error.params.fields.size_bytes).toBe('max');
      expect((await call({ kind: 'bogus', content_type: 'image/png', size_bytes: 5 })).status).toBe(422);
      expect((await call({ kind: 'passport', content_type: 'image/png', size_bytes: 5, driver_id: 'x' })).status).toBe(422);
    });

    it('PUT: soxta fayl (magic bytes), hajm/turi mos emas, imzo buzilgan, qayta yuklash', async () => {
      const jpeg = makeJpeg(true);
      const res = await http().post('/api/v1/drivers/documents/upload-url').set('Authorization', bearer())
        .send({ kind: 'passport', content_type: 'image/jpeg', size_bytes: jpeg.length });
      const url = new URL(res.body.upload_url);
      const path = url.pathname + url.search;
      const put = (p: string, body: Buffer, ct = 'image/jpeg') => http().put(p).set('Content-Type', ct).send(body);

      // PDF baytlari image/jpeg deb yuborilgan (xuddi shu hajmda)
      const fake = Buffer.alloc(jpeg.length, 0x25);
      Buffer.from('%PDF-1.4').copy(fake);
      expect((await put(path, fake)).body.error.params.fields).toEqual({ file: 'invalid_image' });
      expect((await put(path, jpeg.subarray(1))).body.error.params.fields).toEqual({ size_bytes: 'mismatch' });
      expect((await put(path, jpeg, 'image/png')).body.error.params.fields).toEqual({ content_type: 'mismatch' });

      // Birinchi hex belgi har doim boshqasiga almashadi (aks holda 1/16 ehtimol bilan imzo o'zgarmay qolardi)
      const tampered = path.replace(/signature=([0-9a-f])/i, (_m, c: string) => `signature=${c.toLowerCase() === '0' ? '1' : '0'}`);
      expect(tampered).not.toBe(path);
      expect((await put(tampered, jpeg)).status).toBe(403);
      expect((await put(path.replace(/expires=\d+/, 'expires=9999999999'), jpeg)).status).toBe(403);
      expect((await put(`/api/v1/drivers/documents/${res.body.document_id}/content`, jpeg)).status).toBe(403);

      expect((await put(path, jpeg)).status).toBe(204);
      const again = await put(path, jpeg);
      expect(again.status).toBe(409);
      expect(again.body.error.key).toBe('DOCUMENT_ALREADY_UPLOADED');
    });

    it('begona yoki mavjud bo\'lmagan hujjat ID si bilan PUT → 403', async () => {
      const r = await http().put('/api/v1/drivers/documents/33333333-3333-4333-8333-333333333333/content?expires=9999999999&signature=' + 'a'.repeat(64))
        .set('Content-Type', 'image/jpeg').send(makeJpeg(false));
      expect(r.status).toBe(403);
    });
  });

  // ---------------------------------------------------------- egalik (F-10)
  describe('egalik tekshiruvi (F-10)', () => {
    it('3-qadamda boshqa haydovchining hujjati qabul qilinmaydi', async () => {
      const foreignLic = await uploadDoc(USER_B, 'license_front');
      const foreignSelfie = await uploadDoc(USER_B, 'selfie');
      const r = await http().post('/api/v1/drivers/registration/step/3').set('Authorization', bearer(USER_A))
        .send({ documents: [{ kind: 'license_front', document_id: foreignLic }, { kind: 'selfie', document_id: foreignSelfie }] });
      expect(r.status).toBe(422);
      expect(r.body.error.params.fields).toEqual({
        'documents.0.document_id': 'not_found',
        'documents.1.document_id': 'not_found',
      });
      expect((await http().get('/api/v1/drivers/me').set('Authorization', bearer(USER_A))).body.state).toBe('draft');
    });

    it('turi mos kelmasa yoki yuklanmagan bo\'lsa rad etiladi', async () => {
      const a = bearer();
      const lic = await uploadDoc(USER_A, 'license_front');
      const pend = (await http().post('/api/v1/drivers/documents/upload-url').set('Authorization', a)
        .send({ kind: 'selfie', content_type: 'image/png', size_bytes: 10 })).body.document_id;
      const r = await http().post('/api/v1/drivers/registration/step/3').set('Authorization', a)
        .send({ documents: [{ kind: 'selfie', document_id: lic }, { kind: 'license_front', document_id: pend }] });
      expect(r.body.error.params.fields).toEqual({
        'documents.0.kind': 'mismatch',
        'documents.1.kind': 'mismatch',
      });
      const r2 = await http().post('/api/v1/drivers/registration/step/3').set('Authorization', a)
        .send({ documents: [{ kind: 'license_front', document_id: lic }, { kind: 'selfie', document_id: pend }] });
      expect(r2.body.error.params.fields).toEqual({ 'documents.1.document_id': 'not_uploaded' });
    });

    it('har kim faqat o\'z profilini ko\'radi', async () => {
      await completeAll(USER_A);
      const a = await http().get('/api/v1/drivers/me').set('Authorization', bearer(USER_A));
      const b = await http().get('/api/v1/drivers/me').set('Authorization', bearer(USER_B));
      expect(a.body.user_id).toBe(USER_A);
      expect(a.body.first_name).toBe('Ali');
      expect(b.body.user_id).toBe(USER_B);
      expect(b.body.first_name).toBe('');
      expect(b.body.car).toBeUndefined();
      expect(a.body.id).not.toBe(b.body.id);
    });
  });

  // ------------------------------------------------------------------ profil
  describe('profil', () => {
    it('GET /me — OpenAPI DriverProfile maydonlari', async () => {
      await h.store.ensureDriver(USER_A, '+998901234567'); // user.created dan
      const r = await http().get('/api/v1/drivers/me').set('Authorization', bearer());
      expect(r.status).toBe(200);
      expect(r.body).toMatchObject({ user_id: USER_A, phone: '+998901234567', state: 'draft', rating: 0, trips_count: 0, avatar_url: null });
      expect(r.body.id).toMatch(/^[0-9a-f-]{36}$/);
    });

    it('PUT /me — faqat ruxsat etilgan maydonlar (F-09)', async () => {
      const a = bearer();
      const ok = await http().put('/api/v1/drivers/me').set('Authorization', a).send({ first_name: '  Vali ' });
      expect(ok.status).toBe(200);
      expect(ok.body.first_name).toBe('Vali');

      for (const bad of [
        { state: 'active' }, { rating: 5 }, { phone: '+998900000000' }, { id: USER_B }, { user_id: USER_B },
        { trips_count: 100 }, { car: { brand: 'X' } }, { first_name: 'A', state: 'active' },
        { role: 'admin' }, { status: 'active' }, { firstName: 'Camel' }, { last_name: 'B', phone: '+998900000000' },
      ]) {
        const r = await http().put('/api/v1/drivers/me').set('Authorization', a).send(bad);
        expect(r.status).toBe(422);
        expect(r.body.error.key).toBe('VALIDATION_ERROR');
      }
      expect((await http().put('/api/v1/drivers/me').set('Authorization', a).send({})).status).toBe(422);
      expect((await http().put('/api/v1/drivers/me').set('Authorization', a).send({ first_name: '' })).status).toBe(422);
      const me = await http().get('/api/v1/drivers/me').set('Authorization', a);
      expect(me.body).toMatchObject({ state: 'draft', rating: 0, trips_count: 0 });
    });

    it('avatar_document_id — faqat o\'zining avatar hujjati', async () => {
      const a = bearer();
      const mine = await uploadDoc(USER_A, 'avatar');
      const notAvatar = await uploadDoc(USER_A, 'passport');
      const foreign = await uploadDoc(USER_B, 'avatar');
      expect((await http().put('/api/v1/drivers/me').set('Authorization', a).send({ avatar_document_id: mine })).status).toBe(200);
      expect(h.store.drivers.get((await h.store.findByUserId(USER_A))!.id)!.avatarDocumentId).toBe(mine);
      expect((await http().put('/api/v1/drivers/me').set('Authorization', a).send({ avatar_document_id: notAvatar })).body.error.params.fields)
        .toEqual({ avatar_document_id: 'wrong_kind' });
      expect((await http().put('/api/v1/drivers/me').set('Authorization', a).send({ avatar_document_id: foreign })).body.error.params.fields)
        .toEqual({ avatar_document_id: 'not_found' });
    });
  });

  // ------------------------------------------------------------------- holat
  describe('holat', () => {
    it('GET /me/status OpenAPI DriverStatus; haydovchi holatni o\'zgartira olmaydi', async () => {
      const a = bearer();
      const r = await http().get('/api/v1/drivers/me/status').set('Authorization', a);
      expect(r.status).toBe(200);
      expect(r.body).toEqual({ state: 'draft', reason: null, updated_at: expect.any(String) });

      for (const m of ['put', 'post', 'patch', 'delete'] as const) {
        const x = await http()[m]('/api/v1/drivers/me/status').set('Authorization', a).send({ state: 'active' });
        expect(x.status).toBe(404);
      }
      expect((await http().get('/api/v1/drivers/me/status').set('Authorization', a)).body.state).toBe('draft');
    });

    it('bloklangan haydovchi: status ko\'rinadi, boshqa amallar 403 DRIVER_BLOCKED', async () => {
      const d = await h.store.ensureDriver(USER_A);
      await h.store.transition({ id: d.id, from: ['draft'], to: 'blocked', reason: 'documents_expired' });
      const a = bearer();
      const st = await http().get('/api/v1/drivers/me/status').set('Authorization', a);
      expect(st.body).toMatchObject({ state: 'blocked', reason: 'documents_expired' });
      const r = await http().post('/api/v1/drivers/registration/step/1').set('Authorization', a).send(step1);
      expect(r.status).toBe(403);
      expect(r.body).toEqual({ error: { key: 'DRIVER_BLOCKED', params: { reason: 'documents_expired' } } });
      expect((await http().put('/api/v1/drivers/me').set('Authorization', a).send({ first_name: 'X' })).status).toBe(403);
    });
  });
});
