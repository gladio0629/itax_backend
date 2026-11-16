import { readFileSync } from 'fs';
import { join } from 'path';

/** Muhit o'zgaruvchilaridan o'qiladigan sozlamalar. Maxfiy qiymatlar kodda yo'q. */
export interface AppConfig {
  port: number;
  db: { host: string; port: number; user: string; password: string; name: string };
  natsUrl: string;
  jwt: { publicKeyPem: string; issuer: string };
  rolesFile: string;
  tzOffsetHours: number;
  /** operator "resolved" qilgan driver_application uchun driver.approved chiqarilsinmi */
  publishApprovedOnResolved: boolean;
  identity: { mode: 'http' | 'mock'; baseUrl: string; internalToken: string; timeoutMs: number };
  /** driver / identity ichki GET endpointlari (operator navbatidagi telefon uchun) */
  lookups: { mode: 'http' | 'mock'; driverBaseUrl: string };
  /** Identity logout qilgan sessiyalar (`bl:sid:<sid>`) shu Redis'da */
  redis: { url: string; timeoutMs: number };
  /** Trip & Order ichki API (buyurtmalar va buyurtma statistikasi). mock — testlar va kod defaulti */
  orders: { mode: 'http' | 'mock'; baseUrl: string };
}

function required(env: NodeJS.ProcessEnv, key: string): string {
  const v = env[key];
  if (!v) throw new Error(`${key} muhit o'zgaruvchisi berilishi shart`);
  return v;
}

export function loadConfig(env: NodeJS.ProcessEnv = process.env): AppConfig {
  const pem = env.JWT_PUBLIC_KEY_PEM ?? readFileSync(required(env, 'JWT_PUBLIC_KEY_PATH'), 'utf8');
  const identityMode = (env.IDENTITY_STAFF_MODE ?? 'http') as 'http' | 'mock';
  const lookupMode = (env.INTERNAL_LOOKUP_MODE ?? 'mock') as 'http' | 'mock';
  const ordersMode = (env.ORDERS_MODE ?? 'mock') as 'http' | 'mock';
  if (![identityMode, lookupMode, ordersMode].every((m) => ['http', 'mock'].includes(m))) {
    throw new Error("IDENTITY_STAFF_MODE / INTERNAL_LOOKUP_MODE / ORDERS_MODE qiymati 'http' yoki 'mock' bo'lishi kerak");
  }
  // INTERNAL_TOKENS identity bilan bir xil (vergul bilan bir nechta bo'lishi mumkin — yuborishda birinchisi ishlatiladi)
  const internalToken = (env.INTERNAL_TOKENS ?? '').split(',')[0].trim();
  if ((identityMode === 'http' || lookupMode === 'http' || ordersMode === 'http') && internalToken.length < 16) {
    throw new Error("INTERNAL_TOKENS berilishi shart (kamida 16 belgi, identity bilan bir xil)");
  }
  return {
    port: Number(env.PORT ?? 8017),
    identity: {
      mode: identityMode,
      baseUrl: (env.IDENTITY_INTERNAL_URL || 'http://identity:8010').replace(/\/+$/, ''),
      internalToken,
      timeoutMs: Number(env.INTERNAL_HTTP_TIMEOUT_MS ?? 5000),
    },
    lookups: { mode: lookupMode, driverBaseUrl: (env.DRIVER_INTERNAL_URL || 'http://driver:8011').replace(/\/+$/, '') },
    orders: { mode: ordersMode, baseUrl: (env.TRIP_ORDER_INTERNAL_URL || 'http://trip-order:8012').replace(/\/+$/, '') },
    redis: { url: env.REDIS_URL || 'redis://redis:6379', timeoutMs: Number(env.REDIS_TIMEOUT_MS ?? 1000) },
    db: {
      host: required(env, 'DB_HOST'),
      port: Number(env.DB_PORT ?? 5432),
      user: required(env, 'DB_USER'),
      password: required(env, 'DB_PASSWORD'),
      name: required(env, 'DB_NAME'),
    },
    natsUrl: env.NATS_URL || 'nats://nats:4222',
    jwt: { publicKeyPem: pem, issuer: env.JWT_ISSUER || 'izgo-identity' },
    // shared/roles/roles.json — yagona manba. Docker'da ROLES_FILE beriladi.
    rolesFile: env.ROLES_FILE || join(process.cwd(), '..', '..', 'shared', 'roles', 'roles.json'),
    tzOffsetHours: Number(env.TZ_OFFSET_HOURS ?? 5),
    publishApprovedOnResolved: !['0', 'false', 'no'].includes((env.PUBLISH_APPROVED_ON_RESOLVED ?? 'true').toLowerCase()),
  };
}

export const APP_CONFIG = Symbol('APP_CONFIG');
