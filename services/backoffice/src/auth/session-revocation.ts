import { Logger, OnModuleDestroy } from '@nestjs/common';
import Redis from 'ioredis';

/**
 * Bekor qilingan (logout) sessiyalar. Identity `POST /admin/v1/auth/logout` da Redis'ga
 * `bl:sid:<sid>` kalitini (TTL bilan) yozadi — shu kalit bo'lsa token sessiyasi yaroqsiz.
 * Tekshiruv bajarilmasa (Redis ishlamasa) — xato tashlanadi: guard so'rovni RAD ETADI (fail closed).
 */
export abstract class SessionRevocationPort {
  abstract isRevoked(sid: string): Promise<boolean>;
}

export const revokedKey = (sid: string) => `bl:sid:${sid}`;

/** Redis (ioredis) implementatsiyasi. URL va token loglarga yozilmaydi. */
export class RedisSessionRevocationPort extends SessionRevocationPort implements OnModuleDestroy {
  private readonly log = new Logger(RedisSessionRevocationPort.name);
  private readonly redis: Redis;
  private lastErrorLogAt = 0;

  constructor(url: string, timeoutMs = 1000) {
    super();
    this.redis = new Redis(url, {
      // Ishga tushishda ulanish tugaguncha buyruq navbatda kutadi, lekin Redis ishlamasa uzoq kutib
      // qolmaydi: ~timeoutMs ichida xato -> 500 (so'rov o'tkazilmaydi)
      enableOfflineQueue: true,
      maxRetriesPerRequest: 1,
      commandTimeout: timeoutMs,
      connectTimeout: timeoutMs,
      disconnectTimeout: timeoutMs,
      lazyConnect: false,
      retryStrategy: (times) => Math.min(times * 200, 2000),
    });
    this.redis.on('error', (e: Error) => {
      // har bir qayta ulanishda log to'lib ketmasin (URL/parol yozilmaydi)
      const now = Date.now();
      if (now - this.lastErrorLogAt > 30_000) {
        this.lastErrorLogAt = now;
        this.log.error(`redis xatosi: ${e.message}`);
      }
    });
  }

  async isRevoked(sid: string): Promise<boolean> {
    const n = await this.redis.exists(revokedKey(sid));
    return n > 0;
  }

  async onModuleDestroy(): Promise<void> {
    this.redis.disconnect();
  }

  /** testlar uchun */
  close(): void { this.redis.disconnect(); }
}

/** Xotiradagi implementatsiya — testlar uchun (harness). `fail=true` — Redis ishlamayotgandek xato. */
export class InMemorySessionRevocationPort extends SessionRevocationPort {
  readonly revoked = new Set<string>();
  fail = false;

  async isRevoked(sid: string): Promise<boolean> {
    if (this.fail) throw new Error('session revocation store unavailable');
    return this.revoked.has(sid);
  }
}
