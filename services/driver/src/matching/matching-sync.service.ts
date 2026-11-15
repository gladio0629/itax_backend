import { Inject, Injectable, Logger } from '@nestjs/common';
import { INTERNAL_TOKEN_HEADER } from '../auth/internal-token.guard';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { DriverStore } from '../drivers/store/driver.store';

/** POST /internal/matching/drivers/{id}/profile body (matching-geo/openapi.yml: matchingDriverProfileSync). */
export interface MatchingProfileBody {
  driverId: string;
  status: string;
  seats: number | null;
  carClass: string | null;
  /** Yuk tashish turi (spark, damas, ...). Driver servisida hozircha yo'q — doim null. */
  vehicleType: string | null;
  /** Driver servisi joylashuvni bilmaydi (uni ilova to'g'ridan-to'g'ri Matching'ga yuboradi) — doim null. */
  location: { lat: number; lng: number } | null;
}

/**
 * Haydovchi profilini Matching & Geo ga yuboradi (holat, o'rindiqlar, mashina klassi).
 *
 * Matching va Trip & Order haydovchini identity user ID (JWT `sub`) bo'yicha taniydi, shuning uchun
 * path'dagi ID — `driver.userId`, Driver servisining ichki `driver.id` si EMAS.
 * Profil to'liq almashtiriladi (idempotent): bir necha marta chaqirish xavfsiz.
 */
@Injectable()
export class MatchingSync {
  private readonly log = new Logger(MatchingSync.name);

  constructor(
    private readonly store: DriverStore,
    @Inject(APP_CONFIG) private readonly cfg: AppConfig,
  ) {}

  get enabled(): boolean {
    return this.cfg.matching.url !== '';
  }

  /** Bazadagi joriy holatni yuboradi. Xatoda istisno tashlaydi (chaqiruvchi qayta urinishni hal qiladi). */
  async sync(driverId: string): Promise<void> {
    if (!this.enabled) return;
    const d = await this.store.findById(driverId);
    if (!d) {
      // vaqtinchalik xato emas — qayta urinishdan foyda yo'q
      this.log.warn(`matching profil: haydovchi topilmadi (${driverId})`);
      return;
    }
    const car = await this.store.getCar(d.id);
    const body: MatchingProfileBody = {
      driverId: d.userId,
      status: d.state,
      seats: car?.seats ?? null,
      carClass: car?.carClass ?? null,
      vehicleType: null,
      location: null,
    };
    const url = `${this.cfg.matching.url}/internal/matching/drivers/${encodeURIComponent(d.userId)}/profile`;
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', [INTERNAL_TOKEN_HEADER]: this.cfg.internalTokens[0] ?? '' },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(this.cfg.matching.timeoutMs),
    });
    if (!res.ok) {
      const text = (await res.text().catch(() => '')).slice(0, 300);
      throw new Error(`matching profil: HTTP ${res.status} ${text}`);
    }
    this.log.log(`matching profil sinxronlandi: driver=${d.id} user=${d.userId} status=${d.state}`);
  }

  /** Xatoni faqat log'ga yozadi — asosiy amal (tasdiq, profil yangilash) buzilmasligi uchun. */
  async trySync(driverId: string, reason: string): Promise<void> {
    try {
      await this.sync(driverId);
    } catch (err) {
      this.log.warn(`matching profil sinxronlanmadi (${reason}, driver=${driverId}): ${(err as Error).message}`);
    }
  }
}
