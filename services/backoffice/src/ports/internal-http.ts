import { Errors } from '../common/errors';

/** Ichki servislarga (X-Internal-Token bilan) HTTP chaqiruv. Timeout bor, qayta urinish yo'q. */
export class InternalHttpClient {
  constructor(
    private readonly baseUrl: string,
    private readonly token: string,
    private readonly timeoutMs: number,
    private readonly fetchImpl: typeof fetch = fetch,
  ) {}

  /** Tarmoq xatosi yoki timeout — `IDENTITY_UNAVAILABLE` (502). HTTP status ni chaqiruvchi hal qiladi. */
  async request(method: 'GET' | 'POST' | 'PUT', path: string, body?: unknown): Promise<{ status: number; json: any }> {
    const ctl = new AbortController();
    const timer = setTimeout(() => ctl.abort(), this.timeoutMs);
    try {
      const res = await this.fetchImpl(`${this.baseUrl}${path}`, {
        method,
        headers: {
          'X-Internal-Token': this.token,
          Accept: 'application/json',
          ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
        },
        body: body !== undefined ? JSON.stringify(body) : undefined,
        signal: ctl.signal,
      });
      let json: unknown = null;
      try { json = await res.json(); } catch { /* bo'sh yoki JSON bo'lmagan tana */ }
      return { status: res.status, json };
    } catch (e) {
      const timedOut = (e as Error).name === 'AbortError';
      throw Errors.identityUnavailable(timedOut ? 'timeout' : 'unreachable');
    } finally {
      clearTimeout(timer);
    }
  }
}
