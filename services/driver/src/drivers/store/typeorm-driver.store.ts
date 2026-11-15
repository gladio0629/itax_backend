import { Injectable } from '@nestjs/common';
import { InjectDataSource } from '@nestjs/typeorm';
import { DataSource, In, IsNull } from 'typeorm';
import { v4 as uuidv4 } from 'uuid';
import { DriverCapabilities } from '../entities/capabilities.entity';
import { Car } from '../entities/car.entity';
import { DriverDocument } from '../entities/document.entity';
import { Driver, DRIVER_STATES, DriverState } from '../entities/driver.entity';
import { OutboxEvent } from '../entities/outbox.entity';
import { RegistrationStep } from '../entities/registration-step.entity';
import { DriverRoute } from '../entities/route.entity';
import {
  CapabilitiesInput, CreatedRange, DriverProfilePatch, DriverStats, DriverStore, RouteInput, TransitionInput,
} from './driver.store';

@Injectable()
export class TypeOrmDriverStore extends DriverStore {
  constructor(@InjectDataSource() private readonly ds: DataSource) {
    super();
  }

  findById(id: string): Promise<Driver | null> {
    return this.ds.getRepository(Driver).findOne({ where: { id } });
  }

  findByUserId(userId: string): Promise<Driver | null> {
    return this.ds.getRepository(Driver).findOne({ where: { userId } });
  }

  async ensureDriver(userId: string, phone: string | null = null): Promise<Driver> {
    const repo = this.ds.getRepository(Driver);
    // user_id UNIQUE — parallel so'rovlarda ikkinchi INSERT jim o'tkazib yuboriladi
    await repo
      .createQueryBuilder()
      .insert()
      .values({ id: uuidv4(), userId, phone })
      .orIgnore()
      .execute();
    if (phone) {
      await repo
        .createQueryBuilder()
        .update()
        .set({ phone })
        .where('user_id = :userId AND phone IS NULL', { userId })
        .execute();
    }
    return (await this.findByUserId(userId)) as Driver;
  }

  async updateProfile(id: string, patch: DriverProfilePatch): Promise<Driver> {
    await this.ds.getRepository(Driver).update({ id }, patch);
    return (await this.findById(id)) as Driver;
  }

  async saveStep(driverId: string, step: number, data: Record<string, unknown>): Promise<void> {
    await this.ds
      .getRepository(RegistrationStep)
      .upsert({ driverId, step, data, completedAt: new Date() } as never, ['driverId', 'step']);
  }

  getSteps(driverId: string): Promise<RegistrationStep[]> {
    return this.ds.getRepository(RegistrationStep).find({ where: { driverId }, order: { step: 'ASC' } });
  }

  getCar(driverId: string): Promise<Car | null> {
    return this.ds.getRepository(Car).findOne({ where: { driverId } });
  }

  async upsertCar(car: Omit<Car, 'updatedAt'>): Promise<void> {
    await this.ds.getRepository(Car).upsert({ ...car, updatedAt: new Date() }, ['driverId']);
  }

  async createDocument(
    doc: Pick<DriverDocument, 'id' | 'driverId' | 'kind' | 'contentType' | 'sizeBytes'>,
  ): Promise<DriverDocument> {
    const repo = this.ds.getRepository(DriverDocument);
    return repo.save(repo.create({ ...doc, status: 'pending_upload', storageKey: null, uploadedAt: null }));
  }

  findDocument(id: string): Promise<DriverDocument | null> {
    return this.ds.getRepository(DriverDocument).findOne({ where: { id } });
  }

  async markDocumentUploaded(id: string, storageKey: string): Promise<boolean> {
    const res = await this.ds
      .getRepository(DriverDocument)
      .update({ id, status: 'pending_upload' }, { status: 'uploaded', storageKey, uploadedAt: new Date() });
    return (res.affected ?? 0) === 1;
  }

  findDocuments(driverId: string, ids: string[]): Promise<DriverDocument[]> {
    if (ids.length === 0) return Promise.resolve([]);
    return this.ds.getRepository(DriverDocument).find({ where: { driverId, id: In(ids) } });
  }

  async transition(input: TransitionInput): Promise<boolean> {
    return this.ds.transaction(async (em) => {
      const set: Partial<Driver> = {
        state: input.to,
        stateReason: input.reason ?? null,
        stateUpdatedAt: new Date(),
      };
      if (input.submittedAt) set.submittedAt = input.submittedAt;
      const res = await em
        .getRepository(Driver)
        .createQueryBuilder()
        .update()
        .set(set)
        .where('id = :id AND state IN (:...from)', { id: input.id, from: input.from })
        .execute();
      if ((res.affected ?? 0) !== 1) return false;
      if (input.outbox) {
        await em.getRepository(OutboxEvent).insert({ ...input.outbox, publishedAt: null } as never);
      }
      return true;
    });
  }

  listByState(state: DriverState): Promise<Driver[]> {
    return this.ds.getRepository(Driver).find({ where: { state } });
  }

  getRoutes(driverId: string): Promise<DriverRoute[]> {
    return this.ds.getRepository(DriverRoute).find({ where: { driverId }, order: { createdAt: 'ASC', position: 'ASC' } });
  }

  async replaceRoutes(driverId: string, routes: RouteInput[]): Promise<DriverRoute[]> {
    return this.ds.transaction(async (em) => {
      // Haydovchi qatorini qulflaymiz: parallel PUT lar ketma-ket bajariladi (aks holda ikkala to'plam birlashib qolishi mumkin)
      await em.query('SELECT 1 FROM drivers WHERE id = $1 FOR UPDATE', [driverId]);
      const repo = em.getRepository(DriverRoute);
      await repo.delete({ driverId });
      const now = new Date();
      const rows = routes.map((r, i) =>
        repo.create({ id: uuidv4(), driverId, ...r, position: i, createdAt: now }),
      );
      if (rows.length > 0) await repo.insert(rows);
      return rows;
    });
  }

  getCapabilities(driverId: string): Promise<DriverCapabilities | null> {
    return this.ds.getRepository(DriverCapabilities).findOne({ where: { driverId } });
  }

  async upsertCapabilities(driverId: string, caps: CapabilitiesInput): Promise<DriverCapabilities> {
    const repo = this.ds.getRepository(DriverCapabilities);
    await repo.upsert({ driverId, ...caps, updatedAt: new Date() }, ['driverId']);
    return (await this.getCapabilities(driverId)) as DriverCapabilities;
  }

  async stats(range: CreatedRange): Promise<DriverStats> {
    // Bitta so'rov: holat bo'yicha guruhlash + oraliqdagi yangilar (FILTER). pg count ni satr qaytaradi.
    const rows: { state: DriverState; n: string; n_new: string }[] = await this.ds.query(
      `SELECT state,
              count(*) AS n,
              count(*) FILTER (WHERE ($1::timestamptz IS NULL OR created_at >= $1::timestamptz)
                                 AND ($2::timestamptz IS NULL OR created_at <  $2::timestamptz)) AS n_new
         FROM drivers
        GROUP BY state`,
      [range.from ?? null, range.to ?? null],
    );
    const byState = Object.fromEntries(DRIVER_STATES.map((st) => [st, 0])) as Record<DriverState, number>;
    let total = 0;
    let newInRange = 0;
    for (const r of rows) {
      const n = Number(r.n);
      if (r.state in byState) byState[r.state] = n;
      total += n;
      newInRange += Number(r.n_new);
    }
    return { total, byState, newInRange };
  }

  takeUnpublishedOutbox(limit: number): Promise<OutboxEvent[]> {
    return this.ds
      .getRepository(OutboxEvent)
      .find({ where: { publishedAt: IsNull() }, order: { createdAt: 'ASC' }, take: limit });
  }

  async markOutboxPublished(id: string): Promise<void> {
    await this.ds.getRepository(OutboxEvent).update({ id }, { publishedAt: new Date() });
  }
}
