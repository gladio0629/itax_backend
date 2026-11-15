import { v4 as uuidv4 } from 'uuid';
import { DriverCapabilities } from '../../src/drivers/entities/capabilities.entity';
import { Car } from '../../src/drivers/entities/car.entity';
import { DriverDocument } from '../../src/drivers/entities/document.entity';
import { Driver, DRIVER_STATES, DriverState } from '../../src/drivers/entities/driver.entity';
import { OutboxEvent } from '../../src/drivers/entities/outbox.entity';
import { RegistrationStep } from '../../src/drivers/entities/registration-step.entity';
import { DriverRoute } from '../../src/drivers/entities/route.entity';
import {
  CapabilitiesInput, CreatedRange, DriverProfilePatch, DriverStore, RouteInput, TransitionInput,
} from '../../src/drivers/store/driver.store';

/** Testlar uchun: Postgres siz ishlaydi, DriverStore shartnomasini (shartli o'tish, outbox) takrorlaydi. */
export class InMemoryDriverStore extends DriverStore {
  drivers = new Map<string, Driver>();
  steps: RegistrationStep[] = [];
  cars = new Map<string, Car>();
  docs = new Map<string, DriverDocument>();
  outbox: OutboxEvent[] = [];
  routes: DriverRoute[] = [];
  capabilities = new Map<string, DriverCapabilities>();

  async findById(id: string) { return this.drivers.get(id) ?? null; }
  async findByUserId(userId: string) { return [...this.drivers.values()].find((d) => d.userId === userId) ?? null; }

  async ensureDriver(userId: string, phone: string | null = null) {
    let d = await this.findByUserId(userId);
    if (!d) {
      d = Object.assign(new Driver(), {
        id: uuidv4(), userId, phone, firstName: null, lastName: null, birthDate: null, city: null,
        avatarDocumentId: null, state: 'draft', stateReason: null, stateUpdatedAt: new Date(),
        submittedAt: null, rating: 0, tripsCount: 0, createdAt: new Date(), updatedAt: new Date(),
      });
      this.drivers.set(d.id, d);
    } else if (phone && !d.phone) {
      d.phone = phone;
    }
    return d;
  }

  async updateProfile(id: string, patch: DriverProfilePatch) {
    const d = this.drivers.get(id)!;
    Object.assign(d, patch);
    return d;
  }

  async saveStep(driverId: string, step: number, data: Record<string, unknown>) {
    this.steps = this.steps.filter((s) => !(s.driverId === driverId && s.step === step));
    this.steps.push(Object.assign(new RegistrationStep(), { driverId, step, data, completedAt: new Date() }));
  }
  async getSteps(driverId: string) { return this.steps.filter((s) => s.driverId === driverId).sort((a, b) => a.step - b.step); }

  async getCar(driverId: string) { return this.cars.get(driverId) ?? null; }
  async upsertCar(car: Omit<Car, 'updatedAt'>) { this.cars.set(car.driverId, Object.assign(new Car(), car, { updatedAt: new Date() })); }

  async createDocument(doc: Pick<DriverDocument, 'id' | 'driverId' | 'kind' | 'contentType' | 'sizeBytes'>) {
    const d = Object.assign(new DriverDocument(), doc, { status: 'pending_upload', storageKey: null, createdAt: new Date(), uploadedAt: null });
    this.docs.set(d.id, d);
    return d;
  }
  async findDocument(id: string) { return this.docs.get(id) ?? null; }
  async markDocumentUploaded(id: string, storageKey: string) {
    const d = this.docs.get(id);
    if (!d || d.status !== 'pending_upload') return false;
    Object.assign(d, { status: 'uploaded', storageKey, uploadedAt: new Date() });
    return true;
  }
  async findDocuments(driverId: string, ids: string[]) {
    return ids.map((i) => this.docs.get(i)).filter((d): d is DriverDocument => !!d && d.driverId === driverId);
  }

  async transition(i: TransitionInput) {
    const d = this.drivers.get(i.id);
    if (!d || !i.from.includes(d.state)) return false;
    d.state = i.to;
    d.stateReason = i.reason ?? null;
    d.stateUpdatedAt = new Date();
    if (i.submittedAt) d.submittedAt = i.submittedAt;
    if (i.outbox) this.outbox.push(Object.assign(new OutboxEvent(), i.outbox, { createdAt: new Date(), publishedAt: null }));
    return true;
  }
  async listByState(state: DriverState) { return [...this.drivers.values()].filter((d) => d.state === state); }

  async getRoutes(driverId: string) {
    return this.routes.filter((r) => r.driverId === driverId)
      .sort((a, b) => a.createdAt.getTime() - b.createdAt.getTime() || a.position - b.position);
  }
  async replaceRoutes(driverId: string, routes: RouteInput[]) {
    const now = new Date();
    const rows = routes.map((r, i) => Object.assign(new DriverRoute(), { id: uuidv4(), driverId, ...r, position: i, createdAt: now }));
    this.routes = [...this.routes.filter((r) => r.driverId !== driverId), ...rows];
    return rows;
  }

  async getCapabilities(driverId: string) { return this.capabilities.get(driverId) ?? null; }
  async upsertCapabilities(driverId: string, caps: CapabilitiesInput) {
    const c = Object.assign(new DriverCapabilities(), { driverId, ...caps, childSeatAges: [...caps.childSeatAges], updatedAt: new Date() });
    this.capabilities.set(driverId, c);
    return c;
  }

  async stats(range: CreatedRange) {
    const all = [...this.drivers.values()];
    const byState = Object.fromEntries(DRIVER_STATES.map((st) => [st, 0])) as Record<DriverState, number>;
    for (const d of all) byState[d.state] += 1;
    const inRange = (d: Driver) =>
      (!range.from || d.createdAt >= range.from) && (!range.to || d.createdAt < range.to);
    return { total: all.length, byState, newInRange: all.filter(inRange).length };
  }

  async takeUnpublishedOutbox(limit: number) { return this.outbox.filter((o) => !o.publishedAt).slice(0, limit); }
  async markOutboxPublished(id: string) { const o = this.outbox.find((x) => x.id === id); if (o) o.publishedAt = new Date(); }
}
