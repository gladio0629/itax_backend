import { readFileSync } from 'fs';

/** Muhit o'zgaruvchilaridan o'qiladigan sozlamalar. Maxfiy qiymatlar kodda yo'q. */
export interface AppConfig {
  port: number;
  db: { host: string; port: number; user: string; password: string; name: string };
  natsUrl: string;
  jwt: { publicKeyPem: string; issuer: string };
  uploads: {
    dir: string;
    signingSecret: string;
    publicBaseUrl: string;
    urlTtlSeconds: number;
  };
  approval: { autoEnabled: boolean; delayMs: number; systemActorId: string };
  /** /internal/* uchun ruxsat etilgan X-Internal-Token qiymatlari (bo'sh bo'lsa hammasi rad etiladi) */
  internalTokens: string[];
  /** Matching profil sinxronlash. url bo'sh bo'lsa sinxronlash o'chiq (test/offline). */
  matching: { url: string; timeoutMs: number };
}

function required(env: NodeJS.ProcessEnv, key: string): string {
  const v = env[key];
  if (!v) throw new Error(`${key} muhit o'zgaruvchisi berilishi shart`);
  return v;
}

function bool(v: string | undefined, def: boolean): boolean {
  if (v === undefined || v === '') return def;
  return ['1', 'true', 'yes'].includes(v.toLowerCase());
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function loadConfig(env: NodeJS.ProcessEnv = process.env): AppConfig {
  const pem = env.JWT_PUBLIC_KEY_PEM ?? readFileSync(required(env, 'JWT_PUBLIC_KEY_PATH'), 'utf8');
  const secret = required(env, 'UPLOAD_SIGNING_SECRET');
  if (secret.length < 16) throw new Error('UPLOAD_SIGNING_SECRET kamida 16 belgi bo\'lishi kerak');
  const systemActorId = env.APPROVAL_SYSTEM_ACTOR_ID ?? '00000000-0000-0000-0000-000000000001';
  if (!UUID_RE.test(systemActorId)) throw new Error('APPROVAL_SYSTEM_ACTOR_ID UUID bo\'lishi kerak');
  return {
    port: Number(env.PORT ?? 8011),
    db: {
      host: required(env, 'DB_HOST'),
      port: Number(env.DB_PORT ?? 5432),
      user: required(env, 'DB_USER'),
      password: required(env, 'DB_PASSWORD'),
      name: required(env, 'DB_NAME'),
    },
    natsUrl: env.NATS_URL || 'nats://nats:4222',
    jwt: { publicKeyPem: pem, issuer: env.JWT_ISSUER || 'izgo-identity' },
    uploads: {
      dir: env.UPLOADS_DIR || './uploads',
      signingSecret: secret,
      publicBaseUrl: (env.PUBLIC_BASE_URL || 'http://localhost:8000').replace(/\/+$/, ''),
      urlTtlSeconds: Number(env.UPLOAD_URL_TTL_SECONDS ?? 900),
    },
    approval: {
      autoEnabled: bool(env.AUTO_APPROVE_ENABLED, true),
      delayMs: Number(env.AUTO_APPROVE_DELAY_MS ?? 5000),
      systemActorId,
    },
    internalTokens: (env.INTERNAL_TOKENS ?? '').split(',').map((t) => t.trim()).filter(Boolean),
    matching: {
      url: (env.MATCHING_URL ?? '').replace(/\/+$/, ''),
      timeoutMs: Number(env.INTERNAL_HTTP_TIMEOUT_MS || 3000),
    },
  };
}

export const APP_CONFIG = Symbol('APP_CONFIG');
