import { Injectable, Logger } from '@nestjs/common';
import { DriverStore } from '../drivers/store/driver.store';
import { MatchingSync } from '../matching/matching-sync.service';

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const PHONE_RE = /^\+998[0-9]{9}$/;

/** Kiruvchi hodisalar. Barchasi idempotent: bir hodisa ikki marta kelsa zarar yo'q. */
@Injectable()
export class InboundEvents {
  private readonly log = new Logger(InboundEvents.name);

  constructor(
    private readonly store: DriverStore,
    private readonly matching: MatchingSync,
  ) {}

  /** identity: user.created — rol driver bo'lsa telefonni saqlaymiz (profilda `phone` uchun). */
  async onUserCreated(env: unknown): Promise<void> {
    const p = (env as { payload?: Record<string, unknown> })?.payload;
    if (!p || p.role !== 'driver') return; // passenger — bizga aloqasi yo'q
    if (typeof p.user_id !== 'string' || !UUID_RE.test(p.user_id)) throw new SyntaxError('user_id noto\'g\'ri');
    const phone = typeof p.phone === 'string' && PHONE_RE.test(p.phone) ? p.phone : null;
    await this.store.ensureDriver(p.user_id, phone);
  }

  /**
   * backoffice: driver.approved — pending_review → active. Holatni faqat Backoffice (yoki demo
   * rejimidagi avto-tasdiq) o'zgartiradi. Shartli UPDATE: takroriy yoki kech kelgan hodisa hech narsani buzmaydi.
   *
   * So'ng profil Matching'ga yuboriladi — o'tish natijasidan qat'i nazar (avto-tasdiqda holat allaqachon active).
   * Matching ishlamasa istisno tashlanadi: NATS xabarni nak qiladi va keyinroq qayta yuboradi.
   */
  async onDriverApproved(env: unknown): Promise<void> {
    const p = (env as { payload?: Record<string, unknown> })?.payload;
    if (!p || typeof p.driver_id !== 'string' || !UUID_RE.test(p.driver_id)) throw new SyntaxError('driver_id noto\'g\'ri');
    const ok = await this.store.transition({ id: p.driver_id, from: ['pending_review'], to: 'active' });
    if (ok) this.log.log(`driver ${p.driver_id} tasdiqlandi (hodisa orqali)`);
    await this.matching.sync(p.driver_id);
  }
}
