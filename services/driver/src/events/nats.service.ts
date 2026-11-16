import { Inject, Injectable, Logger, OnModuleDestroy, OnModuleInit } from '@nestjs/common';
import {
  connect,
  consumerOpts,
  createInbox,
  JetStreamClient,
  JetStreamManager,
  NatsConnection,
  RetentionPolicy,
  StorageType,
} from 'nats';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { EventPublisher } from './event-publisher';
import { InboundEvents } from './inbound-events';
import { SUBJECT_DRIVER_APPROVED, SUBJECT_USER_CREATED } from './envelope';

const STREAM = 'DRIVER_EVENTS';
const CONSUMER_RETRY_MS = 5000;

/**
 * NATS JetStream: hodisa yuborish (envelope o'zgarishsiz, `{pattern,data}` o'ramisiz) va
 * user.created / driver.approved ni tinglash. Broker ko'tarilmagan bo'lsa servis baribir ishga tushadi —
 * ulanish fonda qayta uriniladi, hodisalar outbox da kutadi.
 */
@Injectable()
export class NatsService extends EventPublisher implements OnModuleInit, OnModuleDestroy {
  private readonly log = new Logger(NatsService.name);
  private nc?: NatsConnection;
  private js?: JetStreamClient;
  private jsm?: JetStreamManager;
  private closed = false;
  private streamReady = false;

  constructor(
    @Inject(APP_CONFIG) private readonly cfg: AppConfig,
    private readonly inbound: InboundEvents,
  ) {
    super();
  }

  async onModuleInit(): Promise<void> {
    void this.connectLoop();
  }

  async onModuleDestroy(): Promise<void> {
    this.closed = true;
    try {
      await this.nc?.drain();
    } catch {
      /* yopilayotgan bo'lishi mumkin */
    }
  }

  private async connectLoop(): Promise<void> {
    while (!this.closed && !this.nc) {
      try {
        this.nc = await connect({
          servers: this.cfg.natsUrl,
          user: this.cfg.nats?.user,
          pass: this.cfg.nats?.pass,
          tls: this.cfg.nats?.caFile ? { caFile: this.cfg.nats.caFile } : undefined,
          name: 'izgo-driver',
          maxReconnectAttempts: -1,
          reconnectTimeWait: 2000,
        });
        this.js = this.nc.jetstream();
        this.jsm = await this.nc.jetstreamManager();
        this.log.log('NATS ga ulandi');
        void this.consumeLoop(SUBJECT_USER_CREATED, 'driver-svc-user-created', (d) => this.inbound.onUserCreated(d));
        void this.consumeLoop(SUBJECT_DRIVER_APPROVED, 'driver-svc-driver-approved', (d) => this.inbound.onDriverApproved(d));
      } catch (err) {
        this.nc = undefined;
        this.log.warn(`NATS ulanmadi, qayta uriniladi: ${(err as Error).message}`);
        await sleep(2000);
      }
    }
  }

  private async ensureStream(): Promise<void> {
    if (this.streamReady || !this.jsm) return;
    try {
      await this.jsm.streams.info(STREAM);
    } catch {
      // stream yo'q — yaratamiz. Xato bo'lsa yutmaymiz: keyingi urinishda qayta uriniladi
      // (boshqa servis shu subject'larni qamrab oluvchi stream yaratgan bo'lsa ham `add` xato beradi — bu holda ham
      // publish ishlaydi, shuning uchun quyida subject bo'yicha stream borligini tekshiramiz)
      try {
        await this.jsm.streams.add({
          name: STREAM,
          subjects: ['izgo.driver.>'],
          storage: StorageType.File,
          retention: RetentionPolicy.Limits,
          max_age: 7 * 24 * 3600 * 1e9,
          duplicate_window: 10 * 60 * 1e9,
        });
      } catch (err) {
        const existing = await this.jsm.streams.find('izgo.driver.submitted.v1').catch(() => null);
        if (!existing) throw err;
        this.log.warn(`${STREAM} yaratilmadi, mavjud stream (${existing}) ishlatiladi`);
      }
    }
    this.streamReady = true;
  }

  async publish(subject: string, payload: Record<string, unknown>, msgId: string): Promise<void> {
    if (!this.js) throw new Error('NATS ga ulanilmagan');
    await this.ensureStream();
    await this.js.publish(subject, new TextEncoder().encode(JSON.stringify(payload)), { msgID: msgId });
  }

  private async consumeLoop(
    subject: string,
    durable: string,
    handler: (data: unknown) => Promise<void>,
  ): Promise<void> {
    while (!this.closed) {
      try {
        if (!this.js) throw new Error('NATS ga ulanilmagan');
        await this.ensureStream();
        const opts = consumerOpts();
        opts.durable(durable);
        opts.manualAck();
        opts.ackExplicit();
        opts.ackWait(30_000);
        opts.deliverTo(createInbox());
        opts.queue(durable);
        opts.filterSubject(subject);
        opts.maxMessages(-1);
        const sub = await this.js.subscribe(subject, opts);
        for await (const m of sub) {
          try {
            await handler(JSON.parse(new TextDecoder().decode(m.data)));
            m.ack();
          } catch (err) {
            this.log.error(`${subject} qayta ishlanmadi: ${(err as Error).message}`);
            // noto'g'ri formatdagi xabarni qayta-qayta yubormaymiz; vaqtinchalik xatoda qayta yuboriladi
            if (err instanceof SyntaxError) m.term();
            else m.nak(5000);
          }
        }
      } catch (err) {
        this.log.warn(`${subject} tinglash qayta uriniladi: ${(err as Error).message}`);
      }
      await sleep(CONSUMER_RETRY_MS);
    }
  }
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

