import { Logger } from '@nestjs/common';
import { AppError, Errors } from '../common/errors';
import type { Page, PageQuery } from '../common/pagination';
import { InternalHttpClient } from './internal-http';
import { CancelReason, DateRange, OrderListFilter, OrderPatch, OrderStatsView, OrdersPort, OrderView } from './ports';

const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0);
const strOrNull = (v: unknown): string | null => (typeof v === 'string' ? v : null);

function qs(params: Record<string, string | number | undefined>): string {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '') u.set(k, String(v));
  const s = u.toString();
  return s ? `?${s}` : '';
}

const isOrder = (j: any): j is OrderView => typeof j === 'object' && j !== null && typeof j.id === 'string';

/**
 * Trip & Order ichki API (X-Internal-Token). Buyurtma obyektlari o'zgarishsiz uzatiladi.
 * Xatolar: 404 -> null (chaqiruvchi ORDER_NOT_FOUND beradi), 403 -> FORBIDDEN (o'sha params.reason),
 * 422 -> VALIDATION_ERROR (o'sha params), tarmoq xatosi / 5xx / kutilmagan javob -> 502 TRIP_ORDER_UNAVAILABLE.
 */
export class HttpOrdersPort extends OrdersPort {
  private readonly log = new Logger(HttpOrdersPort.name);

  constructor(private readonly http: InternalHttpClient) { super(); }

  /** `q` trip-order da yo'q — yuborilmaydi. Cursor trip-order niki (shaffof). */
  async list(f: OrderListFilter, page: PageQuery): Promise<Page<OrderView>> {
    const r = await this.http.request('GET', `/internal/orders${qs({
      status: f.status, from_date: f.fromDate, to_date: f.toDate, passenger_id: f.passengerId,
      cursor: page.cursor, limit: page.limit,
    })}`);
    if (r.status === 200 && Array.isArray(r.json?.data)) {
      return { data: r.json.data, cursor: typeof r.json.cursor === 'string' && r.json.cursor ? r.json.cursor : null };
    }
    throw this.fail('list', r);
  }

  async get(id: string): Promise<OrderView | null> {
    const r = await this.http.request('GET', `/internal/orders/${encodeURIComponent(id)}`);
    if (r.status === 200 && isOrder(r.json)) return r.json;
    if (r.status === 404) return null;
    throw this.fail('get', r);
  }

  async update(id: string, patch: OrderPatch): Promise<OrderView | null> {
    const r = await this.http.request('PATCH', `/internal/orders/${encodeURIComponent(id)}`, patch);
    return this.writeResult('update', r);
  }

  async cancel(id: string, c: { reason: CancelReason; comment?: string }): Promise<OrderView | null> {
    const r = await this.http.request('POST', `/internal/orders/${encodeURIComponent(id)}/cancel`, c);
    return this.writeResult('cancel', r);
  }

  async stats(range: DateRange): Promise<OrderStatsView> {
    const r = await this.http.request('GET', `/internal/orders/stats${qs({ from_date: range.fromDate, to_date: range.toDate })}`);
    if (r.status !== 200 || typeof r.json !== 'object' || r.json === null) throw this.fail('stats', r);
    const j = r.json;
    const byStatus: Record<string, number> = {};
    if (j.orders_by_status && typeof j.orders_by_status === 'object') {
      for (const [k, v] of Object.entries(j.orders_by_status)) byStatus[k] = num(v);
    }
    return {
      orders_total: num(j.orders_total),
      orders_completed: num(j.orders_completed),
      orders_cancelled: num(j.orders_cancelled),
      orders_expired: num(j.orders_expired),
      orders_by_status: byStatus,
      revenue: num(j.revenue),
      commission: num(j.commission),
      average_order_value: num(j.average_order_value),
      daily: (Array.isArray(j.daily) ? j.daily : []).map((d: any) => ({
        date: String(d?.date ?? ''), orders: num(d?.orders), completed: num(d?.completed), revenue: num(d?.revenue),
      })),
      top_routes: (Array.isArray(j.top_routes) ? j.top_routes : []).map((t: any) => ({
        from_city_id: strOrNull(t?.from_city_id), to_city_id: strOrNull(t?.to_city_id),
        from_name: strOrNull(t?.from_name), to_name: strOrNull(t?.to_name), orders: num(t?.orders),
      })),
    };
  }

  private writeResult(op: string, r: { status: number; json: any }): OrderView | null {
    if (r.status === 200 && isOrder(r.json)) return r.json;
    if (r.status === 404) return null;
    throw this.fail(op, r);
  }

  /**
   * 403 (faqat update/cancel — buyurtma holati ruxsat bermaydi) / 422 — trip-order qarori (o'sha params bilan);
   * qolgani — 502. 403 `internal_only` (X-Internal-Token mos emas) — bizning sozlama xatosi: 502.
   */
  private fail(op: string, r: { status: number; json: any }): AppError {
    const err = r.json?.error;
    const params = err && typeof err.params === 'object' && err.params !== null ? err.params : {};
    const writeOp = op === 'update' || op === 'cancel';
    if (r.status === 403 && writeOp && err?.key === 'FORBIDDEN' && params.reason !== 'internal_only') {
      return new AppError(403, 'FORBIDDEN', params);
    }
    if (r.status === 422 && err?.key === 'VALIDATION_ERROR') return new AppError(422, 'VALIDATION_ERROR', params);
    this.log.error(`trip-order ${op}: HTTP ${r.status} ${typeof err?.key === 'string' ? err.key : ''}`);
    return Errors.tripOrderUnavailable(`http_${r.status}`);
  }
}
