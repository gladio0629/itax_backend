import { INestApplication } from '@nestjs/common';
import { Test } from '@nestjs/testing';
import { TypeOrmModule } from '@nestjs/typeorm';
import { generateKeyPairSync, sign as cryptoSign } from 'crypto';
import { newDb } from 'pg-mem';
import { DataSource } from 'typeorm';
import { v4 as uuidv4 } from 'uuid';
import { buildAppModule } from '../../src/app.module';
import { configureApp } from '../../src/app.setup';
import { InMemorySessionRevocationPort, SessionRevocationPort } from '../../src/auth/session-revocation';
import { AppConfig } from '../../src/config/configuration';
import { ENTITIES } from '../../src/entities';
import { Staff } from '../../src/entities/staff.entity';
import { EventPublisher } from '../../src/events/event-publisher';
import { NatsService } from '../../src/events/nats.service';
import { OutboxDispatcher } from '../../src/events/outbox.dispatcher';
import { Init1731450000000 } from '../../src/migrations/1731450000000-init';
import { Complaints1731450000002 } from '../../src/migrations/1731450000002-complaints';
import { MockData } from '../../src/ports/mock-platform';
import { StaffRole } from '../../src/roles/roles.catalog';
import request from 'supertest';

export const ISSUER = 'izgo-identity';
const keys = generateKeyPairSync('ed25519');
const other = generateKeyPairSync('ed25519');
export const PUBLIC_PEM = keys.publicKey.export({ type: 'spki', format: 'pem' }).toString();

export function makeToken(sub: string, claims: Record<string, unknown> = {}, forged = false): string {
  const now = Math.floor(Date.now() / 1000);
  const body = {
    iss: ISSUER, sub, aud: ['admin'], jti: 'jti', sid: 'sid', typ: 'access', iat: now, nbf: now, exp: now + 600, ...claims,
  };
  const h = Buffer.from(JSON.stringify({ alg: 'EdDSA', typ: 'JWT' })).toString('base64url');
  const p = Buffer.from(JSON.stringify(body)).toString('base64url');
  const sig = cryptoSign(null, Buffer.from(`${h}.${p}`), (forged ? other : keys).privateKey).toString('base64url');
  return `${h}.${p}.${sig}`;
}

export class FakePublisher extends EventPublisher {
  sent: { subject: string; payload: Record<string, unknown>; msgId: string }[] = [];
  async publish(subject: string, payload: Record<string, unknown>, msgId: string) { this.sent.push({ subject, payload, msgId }); }
}

export interface Person { id: string; role: StaffRole; token: string; auth: string }

export interface Harness {
  app: INestApplication;
  ds: DataSource;
  mock: MockData;
  publisher: FakePublisher;
  dispatcher: OutboxDispatcher;
  people: Record<string, Person>;
  /** logout qilingan sessiyalar (Redis o'rniga) */
  sessions: InMemorySessionRevocationPort;
  http: () => ReturnType<typeof request>;
  as: (name: string) => {
    get: (u: string) => request.Test; post: (u: string) => request.Test; put: (u: string) => request.Test;
    patch: (u: string) => request.Test; delete: (u: string) => request.Test;
  };
  audit: (action?: string) => Promise<{ action: string; actor_id: string | null; entity_id: string; before: any; after: any }[]>;
}

export async function createHarness(over: Partial<AppConfig> = {}): Promise<Harness> {
  const db = newDb();
  db.public.registerFunction({ name: 'version', returns: 'text' as never, implementation: () => 'PostgreSQL 16' });
  db.public.registerFunction({ name: 'current_database', returns: 'text' as never, implementation: () => 'test' });
  // shikoyatlar migratsiyasidagi CHECK uchun (pg-mem da yo'q)
  db.public.registerFunction({
    name: 'char_length', args: ['text' as never], returns: 'integer' as never, implementation: (s: string) => [...s].length,
  });
  const ds: DataSource = await db.adapters.createTypeormDataSource({
    type: 'postgres', entities: ENTITIES, migrations: [Init1731450000000, Complaints1731450000002], // trigger migratsiyasi plpgsql talab qiladi — pg-mem da yo'q
  });
  await ds.initialize();
  await ds.runMigrations();

  const cfg: AppConfig = {
    port: 0,
    identity: { mode: 'mock', baseUrl: '', internalToken: '', timeoutMs: 1000 },
    lookups: { mode: 'mock', driverBaseUrl: '' },
    orders: { mode: 'mock', baseUrl: '' },
    redis: { url: '', timeoutMs: 1000 },
    db: { host: '', port: 0, user: '', password: '', name: '' }, natsUrl: '',
    jwt: { publicKeyPem: PUBLIC_PEM, issuer: ISSUER }, rolesFile: `${process.cwd()}/../../shared/roles/roles.json`,
    tzOffsetHours: 5, publishApprovedOnResolved: true,
    bootstrap: { email: 'boot@example.com', phone: '+998977777777', name: 'Root' },
    ...over,
  };
  const publisher = new FakePublisher();
  const sessions = new InMemorySessionRevocationPort();
  const dbModule = TypeOrmModule.forRootAsync({
    useFactory: () => ({ type: 'postgres' as const }),
    dataSourceFactory: async () => ds,
  });
  const mod = await Test.createTestingModule({ imports: [buildAppModule(cfg, dbModule)] })
    .overrideProvider(NatsService).useValue({ publish: (s: string, p: Record<string, unknown>, m: string) => publisher.publish(s, p, m) })
    .overrideProvider(EventPublisher).useValue(publisher)
    .overrideProvider(SessionRevocationPort).useValue(sessions)
    .compile();
  const app = mod.createNestApplication({ logger: false });
  configureApp(app);
  await app.init();

  // Xodimlar (DB ga to'g'ridan-to'g'ri)
  const people: Record<string, Person> = {};
  const add = async (name: string, role: StaffRole, extra: Partial<Staff> = {}) => {
    const id = uuidv4();
    const now = new Date();
    await ds.getRepository(Staff).insert({
      id, email: role === 'operator' ? null : `${name}@example.com`, phone: `+9989000${String(Object.keys(people).length).padStart(5, '0')}`,
      fullName: name, role, state: 'active', createdBy: null, createdAt: now, updatedAt: now, ...extra,
    } as never);
    const token = makeToken(id);
    people[name] = { id, role, token, auth: `Bearer ${token}` };
  };
  await add('root', 'superadmin');
  await add('owner', 'owner');
  await add('manager', 'manager');
  await add('manager2', 'manager');
  await add('marketer', 'marketer');
  await add('op1', 'operator');
  await add('op2', 'operator');

  const http = () => request(app.getHttpServer());
  const as = (name: string) => {
    const a = people[name].auth;
    return {
      get: (u: string) => http().get(u).set('Authorization', a),
      post: (u: string) => http().post(u).set('Authorization', a),
      put: (u: string) => http().put(u).set('Authorization', a),
      patch: (u: string) => http().patch(u).set('Authorization', a),
      delete: (u: string) => http().delete(u).set('Authorization', a),
    };
  };
  const audit = async (action?: string) => {
    const rows = await ds.query(`SELECT action, actor_id, entity_id, before, after FROM audit_log ORDER BY created_at, id`);
    return action ? rows.filter((r: { action: string }) => r.action === action) : rows;
  };
  return {
    app, ds, mock: app.get(MockData), publisher, dispatcher: app.get(OutboxDispatcher), people, sessions, http, as, audit,
  };
}
