import { Injectable } from '@nestjs/common';
import { Errors } from '../common/errors';
import { assertNotBlocked } from './drivers.service';
import { CapabilitiesDto, ReplaceRoutesDto } from './dto/request.dto';
import { DriverCapabilitiesResponseDto, DriverRoutesResponseDto } from './dto/response.dto';
import { CHILD_SEAT_AGES, DriverCapabilities } from './entities/capabilities.entity';
import type { Driver } from './entities/driver.entity';
import type { DriverRoute } from './entities/route.entity';
import { DriverStore, RouteInput } from './store/driver.store';

const DEFAULT_CAPABILITIES: DriverCapabilitiesResponseDto = {
  child_seat: false,
  child_seat_ages: [],
  pets: false,
  big_luggage: false,
};

/**
 * "Yo'nalishlarim" va "Imkoniyatlarim". Egalik faqat token orqali (DriverAuthGuard yuklagan haydovchi).
 * Bloklangan haydovchi o'qiy oladi (GET /me kabi), o'zgartira olmaydi — 403 DRIVER_BLOCKED.
 */
@Injectable()
export class DriverPreferencesService {
  constructor(private readonly store: DriverStore) {}

  // ------------------------------------------------------------- yo'nalishlar

  async getRoutes(driver: Driver): Promise<DriverRoutesResponseDto> {
    return toRoutes(await this.store.getRoutes(driver.id));
  }

  async replaceRoutes(driver: Driver, dto: ReplaceRoutesDto): Promise<DriverRoutesResponseDto> {
    assertNotBlocked(driver);
    // Shahar ID lari Trip & Order katalogidan (GET /api/v1/cities). Bu servis katalogni bilmaydi:
    // faqat UUID formati (DTO da) tekshiriladi, shahar mavjudligi tekshirilmaydi.
    // Postgres uuid ni kichik harfda qaytaradi — xotiradagi store bilan bir xil bo'lishi uchun shu yerda normallashtiramiz.
    const routes: RouteInput[] = dto.routes.map((r) => ({
      aCityId: r.a_city_id.toLowerCase(),
      bCityId: r.b_city_id.toLowerCase(),
      bothWays: r.both_ways,
    }));

    const fields: Record<string, string> = {};
    routes.forEach((r, i) => {
      if (r.aCityId === r.bCityId) {
        fields[`routes.${i}.b_city_id`] = 'same_as_a';
        return;
      }
      // Takror: xuddi shu (A,B), yoki ikkisidan biri both_ways bo'lsa — teskari (B,A) ham
      const dup = routes.slice(0, i).some((p) =>
        (p.aCityId === r.aCityId && p.bCityId === r.bCityId) ||
        ((p.bothWays || r.bothWays) && p.aCityId === r.bCityId && p.bCityId === r.aCityId),
      );
      if (dup) fields[`routes.${i}`] = 'duplicate';
    });
    if (Object.keys(fields).length > 0) throw Errors.validation(fields);

    return toRoutes(await this.store.replaceRoutes(driver.id, routes));
  }

  // ------------------------------------------------------------ imkoniyatlar

  async getCapabilities(driver: Driver): Promise<DriverCapabilitiesResponseDto> {
    const c = await this.store.getCapabilities(driver.id);
    return c ? toCapabilities(c) : { ...DEFAULT_CAPABILITIES, child_seat_ages: [] };
  }

  async updateCapabilities(driver: Driver, dto: CapabilitiesDto): Promise<DriverCapabilitiesResponseDto> {
    assertNotBlocked(driver);
    if (!dto.child_seat && dto.child_seat_ages.length > 0) {
      throw Errors.validation({ child_seat_ages: 'requires_child_seat' });
    }
    // Yoshlar doim katalog tartibida saqlanadi (javob deterministik)
    const ages = CHILD_SEAT_AGES.filter((a) => dto.child_seat_ages.includes(a));
    const saved = await this.store.upsertCapabilities(driver.id, {
      childSeat: dto.child_seat,
      childSeatAges: ages,
      pets: dto.pets,
      bigLuggage: dto.big_luggage,
    });
    return toCapabilities(saved);
  }
}

function toRoutes(rows: DriverRoute[]): DriverRoutesResponseDto {
  return {
    routes: rows.map((r) => ({ id: r.id, a_city_id: r.aCityId, b_city_id: r.bCityId, both_ways: r.bothWays })),
  };
}

function toCapabilities(c: DriverCapabilities): DriverCapabilitiesResponseDto {
  return {
    child_seat: c.childSeat,
    child_seat_ages: [...c.childSeatAges],
    pets: c.pets,
    big_luggage: c.bigLuggage,
  };
}
