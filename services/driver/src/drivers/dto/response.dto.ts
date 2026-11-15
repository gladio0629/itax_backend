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
