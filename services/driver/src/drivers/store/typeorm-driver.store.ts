import { Injectable } from '@nestjs/common';
import { InjectDataSource } from '@nestjs/typeorm';
import { DataSource, In, IsNull } from 'typeorm';
import { v4 as uuidv4 } from 'uuid';
import { Car } from '../entities/car.entity';
import { DriverDocument } from '../entities/document.entity';
import { Driver, DriverState } from '../entities/driver.entity';
import { OutboxEvent } from '../entities/outbox.entity';
import { RegistrationStep } from '../entities/registration-step.entity';
import { DriverProfilePatch, DriverStore, TransitionInput } from './driver.store';

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

  takeUnpublishedOutbox(limit: number): Promise<OutboxEvent[]> {
    return this.ds
      .getRepository(OutboxEvent)
      .find({ where: { publishedAt: IsNull() }, order: { createdAt: 'ASC' }, take: limit });
  }

  async markOutboxPublished(id: string): Promise<void> {
    await this.ds.getRepository(OutboxEvent).update({ id }, { publishedAt: new Date() });
  }
}
