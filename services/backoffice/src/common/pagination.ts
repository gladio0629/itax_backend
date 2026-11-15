import { Type } from 'class-transformer';
import { IsInt, IsOptional, IsString, Max, MaxLength, Min } from 'class-validator';
import { Errors } from './errors';

export interface Page<T> {
  data: T[];
  cursor: string | null;
}

/** Barcha ro'yxat endpointlari uchun umumiy so'rov parametrlari (cursor + limit). */
export class PageQuery {
  @IsOptional() @IsString() @MaxLength(512)
  cursor?: string;

  @IsOptional() @Type(() => Number) @IsInt() @Min(1) @Max(100)
  limit?: number;
}

export const DEFAULT_LIMIT = 20;

/** Keyset cursor: (created_at desc, id desc). Noto'g'ri cursor — 422. */
export interface CursorPos { t: number; id: string }

export function encodeCursor(createdAt: Date, id: string): string {
  return Buffer.from(JSON.stringify([createdAt.getTime(), id])).toString('base64url');
}

export function decodeCursor(raw: string | undefined): CursorPos | null {
  if (!raw) return null;
  try {
    const [t, id] = JSON.parse(Buffer.from(raw, 'base64url').toString('utf8'));
    if (typeof t === 'number' && Number.isFinite(t) && typeof id === 'string' && id.length <= 64) return { t, id };
  } catch {
    /* quyida */
  }
  throw Errors.validation({ cursor: 'invalid' });
}

/** limit+1 ta qator olingan massivdan sahifa yasaydi. */
export function toPage<T extends { id: string }>(rows: T[], limit: number, dateOf: (r: T) => Date): Page<T> {
  const more = rows.length > limit;
  const data = more ? rows.slice(0, limit) : rows;
  const last = data[data.length - 1];
  return { data, cursor: more && last ? encodeCursor(dateOf(last), last.id) : null };
}

/** (created_at, id) bo'yicha DESC tartibdagi tayyor massivni sahifalash (mock portlar uchun). */
export function pageArray<T extends { id: string }>(
  all: T[],
  dateOf: (r: T) => Date,
  q: PageQuery,
): Page<T> {
  const limit = q.limit ?? DEFAULT_LIMIT;
  const cur = decodeCursor(q.cursor);
  const sorted = [...all].sort((a, b) => dateOf(b).getTime() - dateOf(a).getTime() || (a.id < b.id ? 1 : -1));
  const rows = sorted.filter((r) => {
    if (!cur) return true;
    const t = dateOf(r).getTime();
    return t < cur.t || (t === cur.t && r.id < cur.id);
  });
  return toPage(rows.slice(0, limit + 1), limit, dateOf);
}

/** TypeORM QueryBuilder uchun cursor sharti (row-value taqqoslashsiz). */
export function applyCursor<Q extends { andWhere: (c: string, p?: Record<string, unknown>) => Q }>(
  qb: Q,
  alias: string,
  cur: CursorPos | null,
  column = 'createdAt',
  asc = false,
): Q {
  if (!cur) return qb;
  const op = asc ? '>' : '<';
  return qb.andWhere(
    `(${alias}.${column} ${op} :curT OR (${alias}.${column} = :curT AND ${alias}.id ${op} :curId))`,
    { curT: new Date(cur.t), curId: cur.id },
  );
}
