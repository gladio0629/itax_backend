import type { ChildSeatAge } from '../entities/capabilities.entity';
import type { DriverState } from '../entities/driver.entity';

export class CarResponseDto {
  brand!: string;
  model!: string;
  year!: number;
  color!: string;
  plate_number!: string;
  car_class!: string;
  seats!: number;
}

export class DriverProfileResponseDto {
  id!: string;
  user_id!: string;
  phone!: string | null;
  first_name!: string;
  last_name!: string;
  avatar_url!: string | null;
  car?: CarResponseDto;
  state!: DriverState;
  rating!: number;
  trips_count!: number;
}

export class RegistrationStateResponseDto {
  current_step!: number;
  completed_steps!: number[];
  state!: DriverState;
}

export class DriverStatusResponseDto {
  state!: DriverState;
  reason!: string | null;
  updated_at!: string;
}

export class UploadUrlResponseDto {
  document_id!: string;
  upload_url!: string;
  method!: 'PUT';
  headers!: Record<string, string>;
  expires_at!: string;
}

/** GET /internal/driver/drivers/{id} — ichki servislar (Backoffice, Trip & Order) uchun. */
export class InternalDriverResponseDto {
  id!: string;
  phone!: string | null;
  firstName!: string | null;
  lastName!: string | null;
  status!: DriverState;
  vehiclePlate!: string | null;
  vehicleModel!: string | null;
  rating!: number;
}

export class DriverRouteResponseDto {
  id!: string;
  a_city_id!: string;
  b_city_id!: string;
  both_ways!: boolean;
}

/** GET/PUT /api/v1/drivers/me/routes */
export class DriverRoutesResponseDto {
  routes!: DriverRouteResponseDto[];
}

/** GET/PUT /api/v1/drivers/me/capabilities */
export class DriverCapabilitiesResponseDto {
  child_seat!: boolean;
  child_seat_ages!: ChildSeatAge[];
  pets!: boolean;
  big_luggage!: boolean;
}

/** GET /internal/driver/stats — Backoffice dashboard uchun. */
export class DriverStatsResponseDto {
  total!: number;
  by_state!: Record<DriverState, number>;
  new_in_range!: number;
}
