import { INestApplication } from '@nestjs/common';
import request from 'supertest';
import { createHarness, Harness, makeToken } from './support/helpers';

const USER_A = '11111111-1111-4111-8111-111111111111';
const USER_B = '22222222-2222-4222-8222-222222222222';
const bearer = (user = USER_A) => `Bearer ${makeToken({ sub: user })}`;

// Shahar ID lari (Trip & Order katalogi) — Driver servisi faqat formatni tekshiradi
const TASHKENT = 'aaaaaaaa-0000-4000-8000-000000000001';
const SAMARKAND = 'aaaaaaaa-0000-4000-8000-000000000002';
const BUKHARA = 'aaaaaaaa-0000-4000-8000-000000000003';
const NIL_SHAPED = '00000000-0000-0000-0000-000000000004'; // versiyasiz UUID ham qabul qilinadi

describe('Yo\'nalishlarim / Imkoniyatlarim (e2e)', () => {
  let h: Harness;
  let app: INestApplication;
  const http = () => request(app.getHttpServer());
  const putRoutes = (body: unknown, user = USER_A) =>
    http().put('/api/v1/drivers/me/routes').set('Authorization', bearer(user)).send(body as object);
  const getRoutes = (user = USER_A) => http().get('/api/v1/drivers/me/routes').set('Authorization', bearer(user));
  const putCaps = (body: unknown, user = USER_A) =>
    http().put('/api/v1/drivers/me/capabilities').set('Authorization', bearer(user)).send(body as object);
  const getCaps = (user = USER_A) => http().get('/api/v1/drivers/me/capabilities').set('Authorization', bearer(user));
  const fields = (r: request.Response) => r.body.error.params.fields;

  beforeEach(async () => { h = await createHarness(); app = h.app; });
  afterEach(async () => { await app.close(); });

  // ------------------------------------------------------------ yo'nalishlar
  describe('GET/PUT /me/routes', () => {
    it('token talab qilinadi', async () => {
      expect((await http().get('/api/v1/drivers/me/routes')).status).toBe(401);
      expect((await http().put('/api/v1/drivers/me/routes').send({ routes: [] })).status).toBe(401);
    });

    it('yangi haydovchida bo\'sh ro\'yxat', async () => {
      const r = await getRoutes();
      expect(r.status).toBe(200);
      expect(r.body).toEqual({ routes: [] });
    });

    it('PUT butun to\'plamni almashtiradi; GET xuddi shu tartibda qaytaradi', async () => {
      const body = {
        routes: [
          { a_city_id: TASHKENT, b_city_id: SAMARKAND, both_ways: true },
          { a_city_id: TASHKENT.toUpperCase(), b_city_id: BUKHARA, both_ways: false },
          { a_city_id: BUKHARA, b_city_id: TASHKENT, both_ways: false }, // teskari, ikkalasi ham bir tomonlama — ruxsat
          { a_city_id: NIL_SHAPED, b_city_id: SAMARKAND, both_ways: false },
        ],
      };
      const put = await putRoutes(body);
      expect(put.status).toBe(200);
      expect(put.body.routes).toHaveLength(4);
      expect(put.body.routes.map(({ id: _id, ...r }: { id: string }) => r)).toEqual([
        { a_city_id: TASHKENT, b_city_id: SAMARKAND, both_ways: true },
        { a_city_id: TASHKENT, b_city_id: BUKHARA, both_ways: false }, // kichik harfga keltirilgan
        { a_city_id: BUKHARA, b_city_id: TASHKENT, both_ways: false },
        { a_city_id: NIL_SHAPED, b_city_id: SAMARKAND, both_ways: false },
      ]);
      for (const r of put.body.routes) expect(r.id).toMatch(/^[0-9a-f-]{36}$/);
      expect(Object.keys(put.body)).toEqual(['routes']);

      const get = await getRoutes();
      expect(get.status).toBe(200);
      expect(get.body).toEqual(put.body);

      // Qayta PUT — eski to'plam butunlay almashtiriladi
      const again = await putRoutes({ routes: [{ a_city_id: SAMARKAND, b_city_id: BUKHARA, both_ways: false }] });
      expect(again.status).toBe(200);
      expect((await getRoutes()).body.routes).toEqual(again.body.routes);
      expect(again.body.routes).toHaveLength(1);

      // Bo'sh massiv — hammasini o'chiradi
      expect((await putRoutes({ routes: [] })).body).toEqual({ routes: [] });
      expect((await getRoutes()).body).toEqual({ routes: [] });
    });

    it('A = B → routes.N.b_city_id: same_as_a', async () => {
      const r = await putRoutes({
        routes: [
          { a_city_id: TASHKENT, b_city_id: SAMARKAND, both_ways: false },
          { a_city_id: BUKHARA, b_city_id: BUKHARA.toUpperCase(), both_ways: true },
        ],
      });
      expect(r.status).toBe(422);
      expect(r.body).toEqual({ error: { key: 'VALIDATION_ERROR', params: { fields: { 'routes.1.b_city_id': 'same_as_a' } } } });
    });

    it('takrorlar: xuddi shu juftlik, both_ways bilan teskari juftlik (keyingi indeks belgilanadi)', async () => {
      const r = await putRoutes({
        routes: [
          { a_city_id: TASHKENT, b_city_id: SAMARKAND, both_ways: false },
          { a_city_id: TASHKENT, b_city_id: SAMARKAND.toUpperCase(), both_ways: true }, // xuddi shu (A,B)
          { a_city_id: BUKHARA, b_city_id: TASHKENT, both_ways: true },
          { a_city_id: TASHKENT, b_city_id: BUKHARA, both_ways: false }, // teskari, oldingisi both_ways
          { a_city_id: SAMARKAND, b_city_id: TASHKENT, both_ways: true }, // teskari, o'zi both_ways
        ],
      });
      expect(r.status).toBe(422);
      expect(fields(r)).toEqual({ 'routes.1': 'duplicate', 'routes.3': 'duplicate', 'routes.4': 'duplicate' });
      // Xato bo'lsa hech narsa saqlanmaydi
      expect((await getRoutes()).body).toEqual({ routes: [] });
    });

    it('10 tadan ko\'p → 422 routes: arrayMaxSize; 10 ta — ruxsat', async () => {
      const ids = Array.from({ length: 12 }, (_, i) => `bbbbbbbb-0000-4000-8000-${String(i).padStart(12, '0')}`);
      const many = (n: number) => ids.slice(1, n + 1).map((b) => ({ a_city_id: ids[0], b_city_id: b, both_ways: false }));
      const ok = await putRoutes({ routes: many(10) });
      expect(ok.status).toBe(200);
      expect(ok.body.routes).toHaveLength(10);
      const bad = await putRoutes({ routes: many(11) });
      expect(bad.status).toBe(422);
      expect(fields(bad)).toEqual({ routes: 'arrayMaxSize' });
      expect((await getRoutes()).body.routes).toHaveLength(10);
    });

    it('noto\'g\'ri UUID → invalid_format; ortiqcha/yetishmayotgan maydonlar → 422', async () => {
      const bad = await putRoutes({ routes: [{ a_city_id: 'toshkent', b_city_id: SAMARKAND, both_ways: false }] });
      expect(bad.status).toBe(422);
      expect(fields(bad)).toEqual({ 'routes.0.a_city_id': 'invalid_format' });
      expect(fields(await putRoutes({ routes: [{ a_city_id: TASHKENT, b_city_id: 123, both_ways: false }] })))
        .toEqual({ 'routes.0.b_city_id': 'invalid_format' });

      const extraNested = await putRoutes({ routes: [{ a_city_id: TASHKENT, b_city_id: SAMARKAND, both_ways: false, driver_id: USER_B }] });
      expect(extraNested.status).toBe(422);
      expect(fields(extraNested)['routes.0.driver_id']).toBeDefined();

      const extraTop = await putRoutes({ routes: [], driver_id: USER_B });
      expect(extraTop.status).toBe(422);
      expect(fields(extraTop).driver_id).toBeDefined();

      const noBothWays = await putRoutes({ routes: [{ a_city_id: TASHKENT, b_city_id: SAMARKAND }] });
      expect(fields(noBothWays)).toEqual({ 'routes.0.both_ways': 'isBoolean' });
      expect(fields(await putRoutes({ routes: [{ a_city_id: TASHKENT, b_city_id: SAMARKAND, both_ways: 'true' }] })))
        .toEqual({ 'routes.0.both_ways': 'isBoolean' });
      expect(fields(await putRoutes({}))).toEqual({ routes: 'isArray' });
      expect(fields(await putRoutes({ routes: 'x' }))).toEqual({ routes: 'isArray' });
      expect(fields(await putRoutes({ routes: [null] }))).toEqual({ 'routes.0': 'nestedValidation' });
      expect((await getRoutes()).body).toEqual({ routes: [] });
    });

    it('egalik: har kim faqat o\'z yo\'nalishlarini ko\'radi va almashtiradi', async () => {
      const a = await putRoutes({ routes: [{ a_city_id: TASHKENT, b_city_id: SAMARKAND, both_ways: true }] }, USER_A);
      expect(a.status).toBe(200);
      expect((await getRoutes(USER_B)).body).toEqual({ routes: [] });

      await putRoutes({ routes: [{ a_city_id: BUKHARA, b_city_id: SAMARKAND, both_ways: false }] }, USER_B);
      // B ning PUT i A nikiga tegmaydi
      expect((await getRoutes(USER_A)).body).toEqual(a.body);
      const b = await getRoutes(USER_B);
      expect(b.body.routes).toHaveLength(1);
      expect(b.body.routes[0]).toMatchObject({ a_city_id: BUKHARA, b_city_id: SAMARKAND });
      expect(b.body.routes[0].id).not.toBe(a.body.routes[0].id);
    });

    it('bloklangan haydovchi: GET ishlaydi, PUT → 403 DRIVER_BLOCKED', async () => {
      await putRoutes({ routes: [{ a_city_id: TASHKENT, b_city_id: SAMARKAND, both_ways: true }] });
      const d = (await h.store.findByUserId(USER_A))!;
      await h.store.transition({ id: d.id, from: ['draft'], to: 'blocked', reason: 'fraud' });
      expect((await getRoutes()).body.routes).toHaveLength(1);
      const r = await putRoutes({ routes: [] });
      expect(r.status).toBe(403);
      expect(r.body).toEqual({ error: { key: 'DRIVER_BLOCKED', params: { reason: 'fraud' } } });
      expect((await getRoutes()).body.routes).toHaveLength(1);
    });

    it('active/pending_review/rejected holatlarida ham o\'zgartirish mumkin', async () => {
      const d = await h.store.ensureDriver(USER_A);
      for (const [from, to] of [['draft', 'pending_review'], ['pending_review', 'active'], ['active', 'rejected']] as const) {
        await h.store.transition({ id: d.id, from: [from], to });
        expect((await putRoutes({ routes: [{ a_city_id: TASHKENT, b_city_id: SAMARKAND, both_ways: false }] })).status).toBe(200);
      }
    });
  });

  // ------------------------------------------------------------ imkoniyatlar
  describe('GET/PUT /me/capabilities', () => {
    const full = { child_seat: true, child_seat_ages: ['3-7', '0-1'], pets: true, big_luggage: false };

    it('standart qiymatlar', async () => {
      const r = await getCaps();
      expect(r.status).toBe(200);
      expect(r.body).toEqual({ child_seat: false, child_seat_ages: [], pets: false, big_luggage: false });
    });

    it('PUT saqlaydi va GET qaytaradi (yoshlar katalog tartibida)', async () => {
      const put = await putCaps(full);
      expect(put.status).toBe(200);
      expect(put.body).toEqual({ child_seat: true, child_seat_ages: ['0-1', '3-7'], pets: true, big_luggage: false });
      expect((await getCaps()).body).toEqual(put.body);

      const off = await putCaps({ child_seat: false, child_seat_ages: [], pets: false, big_luggage: true });
      expect(off.body).toEqual({ child_seat: false, child_seat_ages: [], pets: false, big_luggage: true });
      expect((await getCaps()).body).toEqual(off.body);
      // child_seat=true, yoshlar bo'sh — ruxsat
      expect((await putCaps({ ...full, child_seat_ages: [] })).status).toBe(200);
    });

    it('validatsiya', async () => {
      const r = await putCaps({ ...full, child_seat: false });
      expect(r.status).toBe(422);
      expect(r.body).toEqual({ error: { key: 'VALIDATION_ERROR', params: { fields: { child_seat_ages: 'requires_child_seat' } } } });

      expect(fields(await putCaps({ ...full, child_seat_ages: ['0-1', '0-1'] }))).toEqual({ child_seat_ages: 'arrayUnique' });
      expect(fields(await putCaps({ ...full, child_seat_ages: ['12-18'] }))).toEqual({ child_seat_ages: 'isIn' });
      expect(fields(await putCaps({ ...full, child_seat_ages: '0-1' }))).toEqual({ child_seat_ages: 'isArray' });
      expect(fields(await putCaps({ ...full, pets: 'yes' }))).toEqual({ pets: 'isBoolean' });
      // to'rt maydon ham majburiy
      for (const k of Object.keys(full)) {
        const body: Record<string, unknown> = { ...full };
        delete body[k];
        const x = await putCaps(body);
        expect(x.status).toBe(422);
        expect(fields(x)[k]).toBeDefined();
      }
      const extra = await putCaps({ ...full, driver_id: USER_B });
      expect(extra.status).toBe(422);
      expect(fields(extra).driver_id).toBeDefined();
      expect((await getCaps()).body).toEqual({ child_seat: false, child_seat_ages: [], pets: false, big_luggage: false });
    });

    it('egalik: B A ning imkoniyatlarini ko\'rmaydi', async () => {
      await putCaps(full, USER_A);
      expect((await getCaps(USER_B)).body).toEqual({ child_seat: false, child_seat_ages: [], pets: false, big_luggage: false });
      expect((await getCaps(USER_A)).body.child_seat).toBe(true);
    });

    it('bloklangan haydovchi: GET ishlaydi, PUT → 403 DRIVER_BLOCKED', async () => {
      const d = await h.store.ensureDriver(USER_A);
      await h.store.transition({ id: d.id, from: ['draft'], to: 'blocked', reason: 'fraud' });
      expect((await getCaps()).status).toBe(200);
      const r = await putCaps(full);
      expect(r.status).toBe(403);
      expect(r.body.error.key).toBe('DRIVER_BLOCKED');
    });
  });
});
