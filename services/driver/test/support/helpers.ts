import { Global, INestApplication, Module } from '@nestjs/common';
import { Test } from '@nestjs/testing';
import { generateKeyPairSync, KeyObject, sign as cryptoSign } from 'crypto';
import { deflateSync } from 'zlib';
import { buildAppModule } from '../../src/app.module';
import { configureApp } from '../../src/app.setup';
import { AppConfig } from '../../src/config/configuration';
import { DriverStore } from '../../src/drivers/store/driver.store';
import { FileStorage } from '../../src/documents/storage.service';
import { EventPublisher } from '../../src/events/event-publisher';
import { OutboxDispatcher } from '../../src/events/outbox.dispatcher';
import { InMemoryDriverStore } from './in-memory-store';

export class FakePublisher extends EventPublisher {
  sent: { subject: string; payload: Record<string, unknown>; msgId: string }[] = [];
  async publish(subject: string, payload: Record<string, unknown>, msgId: string) { this.sent.push({ subject, payload, msgId }); }
}

export class MemoryStorage extends FileStorage {
  files = new Map<string, Buffer>();
  async put(key: string, data: Buffer) { this.files.set(key, data); }
  async get(key: string) { return this.files.get(key)!; }
  async delete(key: string) { this.files.delete(key); }
}

export const INTERNAL_TOKEN = 'test-internal-token-123';
export const ISSUER = 'izgo-identity';
const keys = generateKeyPairSync('ed25519');
const other = generateKeyPairSync('ed25519');
export const PUBLIC_PEM = keys.publicKey.export({ type: 'spki', format: 'pem' }).toString();

export function makeToken(
  claims: Record<string, unknown> = {},
  opts: { key?: KeyObject; header?: Record<string, unknown> } = {},
): string {
  const now = Math.floor(Date.now() / 1000);
  const body = {
    iss: ISSUER, sub: '11111111-1111-4111-8111-111111111111', aud: ['driver'], jti: 'jti-1', sid: 'sid-1',
    typ: 'access', role: 'driver', iat: now, nbf: now, exp: now + 600, ...claims,
  };
  const h = Buffer.from(JSON.stringify({ alg: 'EdDSA', typ: 'JWT', ...opts.header })).toString('base64url');
  const p = Buffer.from(JSON.stringify(body)).toString('base64url');
  const sig = cryptoSign(null, Buffer.from(`${h}.${p}`), opts.key ?? keys.privateKey).toString('base64url');
  return `${h}.${p}.${sig}`;
}
export const forgedToken = (claims: Record<string, unknown> = {}) => makeToken(claims, { key: other.privateKey });

export interface Harness {
  app: INestApplication;
  store: InMemoryDriverStore;
  publisher: FakePublisher;
  storage: MemoryStorage;
  dispatcher: OutboxDispatcher;
}

export async function createHarness(over: Partial<AppConfig['approval']> = {}): Promise<Harness> {
  const store = new InMemoryDriverStore();
  const publisher = new FakePublisher();
  const storage = new MemoryStorage();

  @Global()
  @Module({ providers: [{ provide: DriverStore, useValue: store }], exports: [DriverStore] })
  class FakePersistence {}

  const cfg: AppConfig = {
    port: 0,
    db: { host: '', port: 0, user: '', password: '', name: '' },
    natsUrl: '',
    jwt: { publicKeyPem: PUBLIC_PEM, issuer: ISSUER },
    uploads: { dir: '/nonexistent', signingSecret: 'test-signing-secret-123', publicBaseUrl: 'http://gw.test', urlTtlSeconds: 900 },
    approval: { autoEnabled: false, delayMs: 50, systemActorId: '00000000-0000-0000-0000-000000000001', ...over },
    internalTokens: [INTERNAL_TOKEN],
    matching: { url: '', timeoutMs: 1000 },
  };

  // NatsService / LocalFileStorage o'rniga fake lar
  const { EventsModule } = await import('../../src/events/events.module');
  const { NatsService } = await import('../../src/events/nats.service');
  const mod = await Test.createTestingModule({ imports: [buildAppModule(cfg, FakePersistence as never)] })
    .overrideProvider(NatsService).useValue({ publish: (s: string, p: Record<string, unknown>, m: string) => publisher.publish(s, p, m) })
    .overrideProvider(EventPublisher).useValue(publisher)
    .overrideProvider(FileStorage).useValue(storage)
    .compile();
  void EventsModule;
  const app = mod.createNestApplication({ logger: false });
  configureApp(app);
  await app.init();
  return { app, store, publisher, storage, dispatcher: app.get(OutboxDispatcher) };
}

// ------------------------------------------------------------- tasvirlar
function crc32(buf: Buffer): number {
  let c, crc = 0xffffffff;
  for (let n = 0; n < buf.length; n++) {
    c = (crc ^ buf[n]) & 0xff;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    crc = (crc >>> 8) ^ c;
  }
  return (crc ^ 0xffffffff) >>> 0;
}
export function pngChunk(type: string, data: Buffer): Buffer {
  const len = Buffer.alloc(4); len.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, 'latin1'), data]);
  const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(body));
  return Buffer.concat([len, body, crc]);
}
export function makePng(extra: Buffer[] = []): Buffer {
  const ihdr = Buffer.alloc(13); ihdr.writeUInt32BE(1, 0); ihdr.writeUInt32BE(1, 4); ihdr[8] = 8; ihdr[9] = 2;
  const idat = deflateSync(Buffer.from([0, 255, 0, 0]));
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    pngChunk('IHDR', ihdr), ...extra, pngChunk('IDAT', idat), pngChunk('IEND', Buffer.alloc(0)),
  ]);
}
export const EXIF_MARK = 'GPS-LAT-41.31-LNG-69.24';
export function exifPngChunk(): Buffer { return pngChunk('eXIf', Buffer.from(EXIF_MARK)); }

export function seg(marker: number, payload: Buffer): Buffer {
  const len = Buffer.alloc(2); len.writeUInt16BE(payload.length + 2);
  return Buffer.concat([Buffer.from([0xff, marker]), len, payload]);
}
export function makeJpeg(withExif: boolean): Buffer {
  const parts = [Buffer.from([0xff, 0xd8]), seg(0xe0, Buffer.from('JFIF\0\x01\x01\0\0\x01\0\x01\0\0'))];
  if (withExif) parts.push(seg(0xe1, Buffer.from(`Exif\0\0${EXIF_MARK}`)), seg(0xfe, Buffer.from('comment-secret')));
  parts.push(
    seg(0xdb, Buffer.alloc(65, 1)),
    seg(0xc0, Buffer.from([8, 0, 1, 0, 1, 1, 1, 0x11, 0])),
    seg(0xda, Buffer.from([1, 1, 0, 0, 0x3f, 0])),
    Buffer.from([0x12, 0xff, 0x00, 0x34, 0xff, 0xd0, 0x56]), // entropiya: FF00 va RST0 markerlari
    Buffer.from([0xff, 0xd9]),
    Buffer.from('TRAILER-' + EXIF_MARK), // EOI dan keyingi chiqindi
  );
  return Buffer.concat(parts);
}
