import type { AppConfig } from '../config/configuration';
import type { DriverStore } from '../drivers/store/driver.store';
import { MatchingSync } from './matching-sync.service';

const DRIVER_ID = '11111111-1111-4111-8111-111111111111';
const USER_ID = '22222222-2222-4222-8222-222222222222';

function make(url: string, car: unknown = { seats: 4, carClass: 'comfort' }, driver: unknown = { id: DRIVER_ID, userId: USER_ID, state: 'active' }) {
  const store = {
    findById: jest.fn().mockResolvedValue(driver),
    getCar: jest.fn().mockResolvedValue(car),
  } as unknown as DriverStore;
  const cfg = { matching: { url, timeoutMs: 1000 }, internalTokens: ['tok-1', 'tok-2'] } as unknown as AppConfig;
  return new MatchingSync(store, cfg);
}

describe('MatchingSync', () => {
  const realFetch = global.fetch;
  let fetchMock: jest.Mock;

  beforeEach(() => {
    fetchMock = jest.fn().mockResolvedValue(new Response(null, { status: 204 }));
    global.fetch = fetchMock as unknown as typeof fetch;
  });
  afterEach(() => {
    global.fetch = realFetch;
  });

  it('identity user ID bilan POST yuboradi (body: driverId, status, seats, carClass, vehicleType, location)', async () => {
    await make('http://matching-geo:8013').sync(DRIVER_ID);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe(`http://matching-geo:8013/internal/matching/drivers/${USER_ID}/profile`);
    expect(init.method).toBe('POST');
    expect(init.headers['x-internal-token']).toBe('tok-1');
    expect(JSON.parse(init.body)).toEqual({
      driverId: USER_ID,
      status: 'active',
      seats: 4,
      carClass: 'comfort',
      vehicleType: null,
      location: null,
    });
  });

  it('mashina yo\'q bo\'lsa seats/carClass null', async () => {
    await make('http://m', null).sync(DRIVER_ID);
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toMatchObject({ seats: null, carClass: null });
  });

  it('MATCHING_URL bo\'sh — hech narsa yubormaydi', async () => {
    await make('').sync(DRIVER_ID);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('haydovchi topilmasa — jim qaytadi (qayta urinish kerak emas)', async () => {
    await expect(make('http://m', null, null).sync(DRIVER_ID)).resolves.toBeUndefined();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('HTTP xato — sync istisno tashlaydi, trySync esa yutadi', async () => {
    fetchMock.mockResolvedValue(new Response('{"error":{"key":"VALIDATION_ERROR"}}', { status: 422 }));
    const s = make('http://m');
    await expect(s.sync(DRIVER_ID)).rejects.toThrow(/HTTP 422/);
    await expect(s.trySync(DRIVER_ID, 'test')).resolves.toBeUndefined();

    fetchMock.mockRejectedValue(new TypeError('fetch failed'));
    await expect(s.sync(DRIVER_ID)).rejects.toThrow(/fetch failed/);
    await expect(s.trySync(DRIVER_ID, 'test')).resolves.toBeUndefined();
  });
});
