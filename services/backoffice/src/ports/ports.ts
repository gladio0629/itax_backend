import type { Page, PageQuery } from '../common/pagination';

/**
 * Boshqa servislar (driver, identity, trip-order, wallet) bilan aloqa interfeyslari.
 * Hozir mock implementatsiya (mock-platform.ts); keyin HTTP/NATS mijozlari bilan almashtiriladi.
 */

export type AccountState = 'active' | 'blocked' | 'archived';

export interface PersonUpdate {
  first_name?: string;
  last_name?: string;
  state?: AccountState;
  state_reason?: string | null;
}

export interface PersonCreate { phone: string; first_name: string; last_name: string }

export interface AdminDriverView {
  id: string; phone: string; first_name: string; last_name: string;
  state: AccountState; state_reason: string | null;
  driver_state: 'draft' | 'pending_review' | 'active' | 'rejected' | 'blocked';
  car?: { brand: string; model: string; year: number; color: string; plate_number: string; car_class: string; seats: number };
  rating: number; trips_count: number; wallet_balance: number; created_at: string;
}

export interface AdminPassengerView {
  id: string; phone: string; first_name: string | null; last_name: string | null;
  state: AccountState; state_reason: string | null; rating: number | null; orders_count: number; created_at: string;
}

export abstract class DriversPort {
  abstract list(f: { state?: AccountState; q?: string }, page: PageQuery): Promise<Page<AdminDriverView>>;
  abstract get(id: string): Promise<AdminDriverView | null>;
  abstract create(input: PersonCreate): Promise<AdminDriverView>;
  abstract update(id: string, patch: PersonUpdate): Promise<AdminDriverView | null>;
}

export abstract class PassengersPort {
  abstract list(f: { state?: AccountState; q?: string }, page: PageQuery): Promise<Page<AdminPassengerView>>;
  abstract get(id: string): Promise<AdminPassengerView | null>;
  abstract create(input: PersonCreate): Promise<AdminPassengerView>;
  abstract update(id: string, patch: PersonUpdate): Promise<AdminPassengerView | null>;
}

/**
 * Trip & Order dagi buyurtma — o'zgarishsiz uzatiladi (public Order maydonlari + passenger_id, cancel_reason,
 * from_city_id, to_city_id).
 */
export interface OrderView { id: string; status: string; created_at: string; [k: string]: unknown }

/**
 * Sana oralig'i: `from`/`to` — UTC [from, to) (Toshkent vaqti bo'yicha hisoblangan), `fromDate`/`toDate` — so'rovdagi
 * asl YYYY-MM-DD (trip-order ga shu ko'rinishda yuboriladi; Date dan qayta hisoblansa kun siljiydi).
 */
export interface DateRange { from?: Date; to?: Date; fromDate?: string; toDate?: string }

export interface OrderListFilter extends DateRange {
  status?: string;
  passengerId?: string;
  /** Faqat mock: trip-order `q` ni qo'llamaydi — HTTP rejimida yuborilmaydi */
  q?: string;
}

export const PAYMENT_METHODS = ['cash', 'card', 'wallet', 'payme', 'click'] as const;
export const CANCEL_REASONS = ['admin_decision', 'passenger_request', 'driver_request', 'duplicate', 'fraud', 'other'] as const;
export type CancelReason = (typeof CANCEL_REASONS)[number];

export interface OrderPatch {
  comment?: string;
  payment_method?: (typeof PAYMENT_METHODS)[number];
  scheduled_at?: string;
  scheduled_from?: string;
  scheduled_to?: string;
}

export interface OrderStatsView {
  orders_total: number; orders_completed: number; orders_cancelled: number; orders_expired: number;
  orders_by_status: Record<string, number>;
  revenue: number; commission: number; average_order_value: number;
  daily: { date: string; orders: number; completed: number; revenue: number }[];
  top_routes: { from_city_id: string | null; to_city_id: string | null; from_name: string | null; to_name: string | null; orders: number }[];
}

/**
 * Trip & Order ichki API (ORDERS_MODE=http) yoki mock.
 * update/cancel: buyurtma yo'q — null; tahrirlab/bekor qilib bo'lmaydi — 403 FORBIDDEN {reason}; validatsiya — 422.
 */
export abstract class OrdersPort {
  abstract list(f: OrderListFilter, page: PageQuery): Promise<Page<OrderView>>;
  abstract get(id: string): Promise<OrderView | null>;
  abstract update(id: string, patch: OrderPatch): Promise<OrderView | null>;
  abstract cancel(id: string, c: { reason: CancelReason; comment?: string }): Promise<OrderView | null>;
  abstract stats(range: DateRange): Promise<OrderStatsView>;
}

export interface LedgerEntryView {
  id: string; wallet_id: string; kind: string; amount: number; balance_after: number;
  reference_id: string | null; created_at: string;
}

export abstract class WalletPort {
  abstract ledger(f: { walletId?: string; driverId?: string }, page: PageQuery): Promise<Page<LedgerEntryView>>;
  abstract walletExists(walletId: string): Promise<boolean>;
  /** Tasdiqlangan tuzatishni daftarga yozadi. correctionId bo'yicha idempotent. */
  abstract applyCorrection(c: { correctionId: string; walletId: string; amount: number }): Promise<void>;
}

/**
 * Haydovchi/yo'lovchi ko'rsatkichlari (driver / identity servislaridan). HOZIR MOCK — haqiqiy manba ulanmagan.
 * Buyurtma/daromad ko'rsatkichlari OrdersPort.stats dan olinadi.
 */
export interface PlatformStatsView {
  active_drivers: number; online_drivers: number; new_passengers: number; drivers_over_limit: number;
}

/** GET /admin/v1/stats javobi */
export type StatsView = OrderStatsView & PlatformStatsView;

export abstract class StatsPort {
  abstract stats(range: DateRange): Promise<PlatformStatsView>;
}

export interface IdentityStaff {
  id: string; email: string | null; phone: string | null; fullName: string;
  role: string; active: boolean;
}

/**
 * Xodim identity'da ham bo'lishi shart (kirish shu yerda). Identity xodim ID sini O'ZI beradi
 * (token `sub` shu ID) — Backoffice uni qabul qilib, o'z yozuviga ishlatadi.
 * Xatolar: band email/telefon — 409 (STAFF_EMAIL_TAKEN / STAFF_PHONE_TAKEN), identity ishlamasa — 502.
 */
export abstract class IdentityStaffPort {
  abstract create(s: { phone: string; email: string | null; role: string; name: string }): Promise<{ id: string }>;
  /**
   * Rol/telefon/ism/holat o'zgarishi: PUT /internal/identity/staff/{id}. Xato bo'lsa chaqiruvchi tranzaksiyasi
   * bekor bo'ladi.
   */
  abstract update(staff: IdentityStaff): Promise<void>;
}

/** GET /internal/driver/drivers/{id} — haydovchi ma'lumoti (operator navbatida telefon uchun). */
export abstract class DriverLookupPort {
  abstract getDriver(id: string): Promise<{ id: string; phone: string | null } | null>;
}

/** GET /internal/identity/users/{id} — yo'lovchi (foydalanuvchi) ma'lumoti. */
export abstract class UserLookupPort {
  abstract getUser(id: string): Promise<{ id: string; phone: string | null } | null>;
}
