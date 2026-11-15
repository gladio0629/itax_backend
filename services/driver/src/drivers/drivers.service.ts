import { Injectable } from '@nestjs/common';
import { Errors } from '../common/errors';
import { MatchingSync } from '../matching/matching-sync.service';
import {
  CarResponseDto, DriverProfileResponseDto, DriverStatsResponseDto, DriverStatusResponseDto, InternalDriverResponseDto,
} from './dto/response.dto';
import { UpdateProfileDto } from './dto/request.dto';
import type { Car } from './entities/car.entity';
import type { Driver } from './entities/driver.entity';
import { CreatedRange, DriverStore } from './store/driver.store';

export function assertNotBlocked(driver: Driver): void {
  if (driver.state === 'blocked') throw Errors.driverBlocked(driver.stateReason);
}

@Injectable()
export class DriversService {
  constructor(
    private readonly store: DriverStore,
    private readonly matching: MatchingSync,
  ) {}

  async getProfile(driver: Driver): Promise<DriverProfileResponseDto> {
    const fresh = (await this.store.findById(driver.id)) ?? driver;
    return this.toProfile(fresh, await this.store.getCar(fresh.id));
  }

  /** Ichki lookup: boshqa servislar uchun haydovchining qisqa ma'lumoti. */
  async getInternal(id: string): Promise<InternalDriverResponseDto> {
    const d = await this.store.findById(id);
    if (!d) throw Errors.driverNotFound(id);
    const car = await this.store.getCar(id);
    return {
      id: d.id,
      phone: d.phone,
      firstName: d.firstName,
      lastName: d.lastName,
      status: d.state,
      vehiclePlate: car?.plateNumber ?? null,
      vehicleModel: car ? `${car.brand} ${car.model}` : null,
      rating: d.rating,
    };
  }

  /** F-09: faqat DTO dagi maydonlar; holat/rating/mashina bu yerda o'zgarmaydi. */
  async updateProfile(driver: Driver, dto: UpdateProfileDto): Promise<DriverProfileResponseDto> {
    assertNotBlocked(driver);
    const patch: { firstName?: string; lastName?: string; avatarDocumentId?: string } = {};
    if (dto.first_name !== undefined) patch.firstName = dto.first_name;
    if (dto.last_name !== undefined) patch.lastName = dto.last_name;
    if (dto.avatar_document_id !== undefined) {
      // F-10: hujjat shu haydovchiniki, yuklangan va turi avatar bo'lishi shart
      const [doc] = await this.store.findDocuments(driver.id, [dto.avatar_document_id]);
      if (!doc) throw Errors.validation({ avatar_document_id: 'not_found' });
      if (doc.kind !== 'avatar') throw Errors.validation({ avatar_document_id: 'wrong_kind' });
      if (doc.status !== 'uploaded') throw Errors.validation({ avatar_document_id: 'not_uploaded' });
      patch.avatarDocumentId = doc.id;
    }
    if (Object.keys(patch).length === 0) throw Errors.validation({ body: 'empty' });

    const updated = await this.store.updateProfile(driver.id, patch);
    // Matching profili (holat + mashina) har muvaffaqiyatli yangilanishda qayta yuboriladi (idempotent).
    // Hozir mashina bu yerda o'zgarmaydi (F-09), lekin maydonlar qo'shilsa sinxronlash allaqachon ulangan.
    await this.matching.trySync(driver.id, 'profile_update');
    return this.toProfile(updated, await this.store.getCar(driver.id));
  }

  getStatus(driver: Driver): DriverStatusResponseDto {
    return {
      state: driver.state,
      reason: driver.stateReason,
      updated_at: driver.stateUpdatedAt.toISOString(),
    };
  }

  async getStatusFresh(driver: Driver): Promise<DriverStatusResponseDto> {
    return this.getStatus((await this.store.findById(driver.id)) ?? driver);
  }

  /** Ichki statistika (Backoffice dashboard): holatlar bo'yicha va davrda yaratilganlar. */

  async stats(fromDate?: string, toDate?: string): Promise<DriverStatsResponseDto> {
    const range: CreatedRange = {};
    const fields: Record<string, string> = {};
    if (fromDate !== undefined) {
      const d = parseUtcDate(fromDate);
      if (d) range.from = d;
      else fields.from_date = 'invalid_date';
    }
    if (toDate !== undefined) {
      const d = parseUtcDate(toDate);
      // to_date kuni ham kiradi: [from 00:00, to+1 00:00)
      if (d) range.to = new Date(d.getTime() + 24 * 60 * 60 * 1000);
      else fields.to_date = 'invalid_date';
    }
    if (Object.keys(fields).length > 0) throw Errors.validation(fields);
    if (range.from && range.to && range.from >= range.to) throw Errors.validation({ to_date: 'before_from_date' });

    const s = await this.store.stats(range);
    return { total: s.total, by_state: s.byState, new_in_range: s.newInRange };
  }

  private toProfile(d: Driver, car: Car | null): DriverProfileResponseDto {
    const out: DriverProfileResponseDto = {
      id: d.id,
      user_id: d.userId,
      phone: d.phone,
      first_name: d.firstName ?? '',
      last_name: d.lastName ?? '',
      // Fayllar hozir lokal; ommaviy URL S3/CDN ga o'tilganda beriladi
      avatar_url: null,
      state: d.state,
      rating: d.rating,
      trips_count: d.tripsCount,
    };
    if (car) out.car = toCar(car);
    return out;
  }
}

function toCar(c: Car): CarResponseDto {
  return {
    brand: c.brand,
    model: c.model,
    year: c.year,
    color: c.color,
    plate_number: c.plateNumber,
    car_class: c.carClass,
    seats: c.seats,
  };
}

/** YYYY-MM-DD → shu kunning 00:00 UTC; noto'g'ri format yoki mavjud bo'lmagan sana (2026-02-31) — null. */
export function parseUtcDate(value: string): Date | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return null;
  const [y, m, day] = value.split('-').map(Number);
  const dt = new Date(Date.UTC(y, m - 1, day));
  if (dt.getUTCFullYear() !== y || dt.getUTCMonth() !== m - 1 || dt.getUTCDate() !== day) return null;
  return dt;
}
