import { Injectable, Logger, OnModuleDestroy, OnModuleInit } from '@nestjs/common';
import { DriverStore } from '../drivers/store/driver.store';
import { EventPublisher } from './event-publisher';

/** Outbox dagi yuborilmagan hodisalarni NATS ga o'tkazadi (kamida bir marta; event_id bilan deduplikatsiya). */
@Injectable()
export class OutboxDispatcher implements OnModuleInit, OnModuleDestroy {
  private readonly log = new Logger(OutboxDispatcher.name);
  private timer?: NodeJS.Timeout;
  private running = false;

  constructor(
    private readonly store: DriverStore,
    private readonly publisher: EventPublisher,
  ) {}

  onModuleInit(): void {
    this.timer = setInterval(() => void this.drain(), 1000);
    this.timer.unref();
  }

  onModuleDestroy(): void {
    if (this.timer) clearInterval(this.timer);
  }

  /** Ketma-ket tartibda yuboradi; birinchi xatoda to'xtaydi (tartib saqlanadi). */
  async drain(limit = 50): Promise<number> {
    if (this.running) return 0;
    this.running = true;
    let sent = 0;
    try {
      const batch = await this.store.takeUnpublishedOutbox(limit);
      for (const ev of batch) {
        try {
          await this.publisher.publish(ev.subject, ev.payload, ev.id);
        } catch (err) {
          this.log.warn(`publish ${ev.subject} muvaffaqiyatsiz, keyinroq qayta uriniladi: ${(err as Error).message}`);
          break;
        }
        await this.store.markOutboxPublished(ev.id);
        sent++;
      }
    } catch (err) {
      this.log.error(`outbox o'qishda xato: ${(err as Error).message}`);
    } finally {
      this.running = false;
    }
    return sent;
  }
}
