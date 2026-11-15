import { Inject, Injectable, Logger, OnApplicationBootstrap, OnModuleDestroy } from '@nestjs/common';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { DriverStore } from '../drivers/store/driver.store';
import { driverApprovedEvent } from '../events/envelope';
import { MatchingSync } from '../matching/matching-sync.service';

/**
 * DEMO: ariza yuborilgach `AUTO_APPROVE_DELAY_MS` (5s) dan keyin avtomatik tasdiqlaydi.
 * Haqiqiy tekshiruv (Backoffice / doc-scanner) kelganda AUTO_APPROVE_ENABLED=false qilinadi va
 * holatni `izgo.driver.approved.v1` hodisasi o'zgartiradi (InboundEvents).
 */
@Injectable()
export class ApprovalService implements OnApplicationBootstrap, OnModuleDestroy {
  private readonly log = new Logger(ApprovalService.name);
  private readonly timers = new Map<string, NodeJS.Timeout>();

  constructor(
    private readonly store: DriverStore,
    @Inject(APP_CONFIG) private readonly cfg: AppConfig,
    private readonly matching: MatchingSync,
  ) {}

  /** Qayta ishga tushganda tekshiruvda qolib ketgan arizalarni qayta rejalashtiradi. */
  async onApplicationBootstrap(): Promise<void> {
    if (!this.cfg.approval.autoEnabled) return;
    const pending = await this.store.listByState('pending_review');
    for (const d of pending) {
      const elapsed = d.submittedAt ? Date.now() - d.submittedAt.getTime() : 0;
      this.schedule(d.id, Math.max(0, this.cfg.approval.delayMs - elapsed));
    }
  }

  onModuleDestroy(): void {
    for (const t of this.timers.values()) clearTimeout(t);
    this.timers.clear();
  }

  schedule(driverId: string, delayMs: number = this.cfg.approval.delayMs): void {
    if (!this.cfg.approval.autoEnabled || this.timers.has(driverId)) return;
    const t = setTimeout(() => {
      this.timers.delete(driverId);
      void this.approve(driverId).catch((err: Error) =>
        this.log.error(`avto-tasdiq xatosi (${driverId}): ${err.message}`),
      );
    }, delayMs);
    t.unref();
    this.timers.set(driverId, t);
  }

  /**
   * Shartli o'tish: faqat pending_review → active. Hodisa shu tranzaksiyada outbox ga yoziladi.
   * O'tish muvaffaqiyatli bo'lsa profil darhol Matching'ga yuboriladi. Bu yerda xato faqat log'ga yoziladi:
   * driver.approved hodisasini Driver o'zi ham tinglaydi (InboundEvents) va u yerda xatoda qayta uriniladi.
   */
  async approve(driverId: string): Promise<boolean> {
    const ev = driverApprovedEvent({
      driverId,
      approvedBy: this.cfg.approval.systemActorId,
      approvedAt: new Date(),
    });
    const moved = await this.store.transition({ id: driverId, from: ['pending_review'], to: 'active', outbox: ev });
    if (moved) await this.matching.trySync(driverId, 'approved');
    return moved;
  }
}
