import type { Driver, DriverState } from '../entities/driver.entity';
import type { Car } from '../entities/car.entity';
import type { DriverDocument } from '../entities/document.entity';
import type { RegistrationStep } from '../entities/registration-step.entity';
import type { OutboxEvent } from '../entities/outbox.entity';
import type { DriverRoute } from '../entities/route.entity';
import type { DriverCapabilities } from '../entities/capabilities.entity';

export type DriverProfilePatch = Partial<
  Pick<Driver, 'firstName' | 'lastName' | 'birthDate' | 'city' | 'avatarDocumentId'>
>;

export type RouteInput = Pick<DriverRoute, 'aCityId' | 'bCityId' | 'bothWays'>;
export type CapabilitiesInput = Omit<DriverCapabilities, 'driverId' | 'updatedAt'>;

/** created_at oralig'i: [from, to) — ikkalasi ham ixtiyoriy */
export interface CreatedRange {
  from?: Date;
  to?: Date;
}

export interface DriverStats {
  total: number;
  byState: Record<DriverState, number>;
  newInRange: number;
}

export interface TransitionInput {
  id: string;
  from: DriverState[];
  to: DriverState;
  reason?: string | null;
  submittedAt?: Date;
  /** Holat o'zgarishi bilan bir tranzaksiyada outbox ga yoziladi */
  outbox?: Pick<OutboxEvent, 'id' | 'subject' | 'payload'>;
}

/**
 * Ma'lumotlar qatlami. Servislar faqat shu abstraksiyaga tayanadi:
 * ishlab chiqarishda TypeORM (PostgreSQL), testlarda xotiradagi implementatsiya.
 */
export abstract class DriverStore {
  abstract findById(id: string): Promise<Driver | null>;
  abstract findByUserId(userId: string): Promise<Driver | null>;
  /** Yozuv bo'lmasa draft holatida yaratadi (telefon ma'lum bo'lsa to'ldiradi). Parallel chaqiruvlarga chidamli. */
  abstract ensureDriver(userId: string, phone?: string | null): Promise<Driver>;
  abstract updateProfile(id: string, patch: DriverProfilePatch): Promise<Driver>;

  abstract saveStep(driverId: string, step: number, data: Record<string, unknown>): Promise<void>;
  abstract getSteps(driverId: string): Promise<RegistrationStep[]>;

  abstract getCar(driverId: string): Promise<Car | null>;
  abstract upsertCar(car: Omit<Car, 'updatedAt'>): Promise<void>;

  abstract createDocument(doc: Pick<DriverDocument, 'id' | 'driverId' | 'kind' | 'contentType' | 'sizeBytes'>): Promise<DriverDocument>;
  abstract findDocument(id: string): Promise<DriverDocument | null>;
  /** pending_upload → uploaded (atomik). false — allaqachon yuklangan. */
  abstract markDocumentUploaded(id: string, storageKey: string): Promise<boolean>;
  abstract findDocuments(driverId: string, ids: string[]): Promise<DriverDocument[]>;

  /** Shartli holat o'tkazish: faqat joriy holat `from` ichida bo'lsagina. true — o'tkazildi. */
  abstract transition(input: TransitionInput): Promise<boolean>;
  abstract listByState(state: DriverState): Promise<Driver[]>;

  /** Yaratilish (massiv) tartibida */
  abstract getRoutes(driverId: string): Promise<DriverRoute[]>;
  /** Butun to'plamni atomik almashtiradi (parallel PUT lar aralashmaydi) va yangi ro'yxatni qaytaradi. */
  abstract replaceRoutes(driverId: string, routes: RouteInput[]): Promise<DriverRoute[]>;

  /** null — hali saqlanmagan (standart qiymatlar) */
  abstract getCapabilities(driverId: string): Promise<DriverCapabilities | null>;
  abstract upsertCapabilities(driverId: string, caps: CapabilitiesInput): Promise<DriverCapabilities>;

  /** Holatlar bo'yicha soni (barcha holatlar, 0 bilan) + `range` ichida yaratilganlar soni. */
  abstract stats(range: CreatedRange): Promise<DriverStats>;

  abstract takeUnpublishedOutbox(limit: number): Promise<OutboxEvent[]>;
  abstract markOutboxPublished(id: string): Promise<void>;
}
