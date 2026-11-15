import { Errors } from './errors';

/** Sanalar Toshkent vaqtida (UTC+5, DST yo'q); offset sozlanadi. */
export function dayStartUtc(date: string, offsetHours: number): Date {
  const [y, m, d] = date.split('-').map(Number);
  return new Date(Date.UTC(y, m - 1, d) - offsetHours * 3600_000);
}

export function isRealDate(s: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(s)) return false;
  const [y, m, d] = s.split('-').map(Number);
  const dt = new Date(Date.UTC(y, m - 1, d));
  return dt.getUTCFullYear() === y && dt.getUTCMonth() === m - 1 && dt.getUTCDate() === d;
}

/** [from, to) UTC oralig'i: from_date 00:00 dan to_date ning keyingi kuni 00:00 gacha (mahalliy). */
export function dateRange(
  fromDate: string | undefined,
  toDate: string | undefined,
  offsetHours: number,
): { from?: Date; to?: Date } {
  const out: { from?: Date; to?: Date } = {};
  if (fromDate) out.from = dayStartUtc(fromDate, offsetHours);
  if (toDate) out.to = new Date(dayStartUtc(toDate, offsetHours).getTime() + 86400_000);
  return out;
}

/** Hozirgi mahalliy kunning [boshi, oxiri) UTC oralig'i. */
export function todayRange(now: Date, offsetHours: number): { from: Date; to: Date } {
  const local = new Date(now.getTime() + offsetHours * 3600_000);
  const day = local.toISOString().slice(0, 10);
  const from = dayStartUtc(day, offsetHours);
  return { from, to: new Date(from.getTime() + 86400_000) };
}

/** from_date/to_date so'rov parametrlarini tekshirib, UTC oralig'iga aylantiradi (422 — noto'g'ri sana). */
export function parseRange(
  fromDate: string | undefined,
  toDate: string | undefined,
  offsetHours: number,
): { from?: Date; to?: Date } {
  const fields: Record<string, string> = {};
  if (fromDate && !isRealDate(fromDate)) fields.from_date = 'invalid_date';
  if (toDate && !isRealDate(toDate)) fields.to_date = 'invalid_date';
  if (!Object.keys(fields).length && fromDate && toDate && toDate < fromDate) fields.to_date = 'before_from_date';
  if (Object.keys(fields).length) throw Errors.validation(fields);
  return dateRange(fromDate, toDate, offsetHours);
}
