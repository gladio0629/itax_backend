import { Body, Controller, Get, Headers, HttpCode, Param, Post, Put, UseGuards } from '@nestjs/common';
import { CurrentDriver, DriverAuthGuard } from '../auth/auth.guard';
import { StepPipe } from '../common/pipes';
import { DriversService } from './drivers.service';
import { CapabilitiesDto, ReplaceRoutesDto, UpdateProfileDto } from './dto/request.dto';
import {
  DriverCapabilitiesResponseDto, DriverProfileResponseDto, DriverRoutesResponseDto, DriverStatusResponseDto,
  RegistrationStateResponseDto,
} from './dto/response.dto';
import type { Driver } from './entities/driver.entity';
import { DriverPreferencesService } from './preferences.service';
import { RegistrationService } from './registration.service';

@Controller('api/v1/drivers')
@UseGuards(DriverAuthGuard)
export class DriversController {
  constructor(
    private readonly drivers: DriversService,
    private readonly registration: RegistrationService,
    private readonly preferences: DriverPreferencesService,
  ) {}

  @Get('me')
  getMe(@CurrentDriver() driver: Driver): Promise<DriverProfileResponseDto> {
    return this.drivers.getProfile(driver);
  }

  @Put('me')
  updateMe(@CurrentDriver() driver: Driver, @Body() dto: UpdateProfileDto): Promise<DriverProfileResponseDto> {
    return this.drivers.updateProfile(driver, dto);
  }

  /** Haydovchi holatini faqat o'qiy oladi; o'zgartirish endpointi yo'q (faqat Backoffice). */
  @Get('me/status')
  getStatus(@CurrentDriver() driver: Driver): Promise<DriverStatusResponseDto> {
    return this.drivers.getStatusFresh(driver);
  }

  /** "Yo'nalishlarim" — shaharlararo yo'nalishlar (yaratilish tartibida). */
  @Get('me/routes')
  getRoutes(@CurrentDriver() driver: Driver): Promise<DriverRoutesResponseDto> {
    return this.preferences.getRoutes(driver);
  }

  /** Butun to'plamni atomik almashtiradi (0..10 ta). */
  @Put('me/routes')
  replaceRoutes(@CurrentDriver() driver: Driver, @Body() dto: ReplaceRoutesDto): Promise<DriverRoutesResponseDto> {
    return this.preferences.replaceRoutes(driver, dto);
  }

  /** "Imkoniyatlarim" — bolalar o'rindig'i, uy hayvonlari, katta yuk. */
  @Get('me/capabilities')
  getCapabilities(@CurrentDriver() driver: Driver): Promise<DriverCapabilitiesResponseDto> {
    return this.preferences.getCapabilities(driver);
  }

  @Put('me/capabilities')
  updateCapabilities(@CurrentDriver() driver: Driver, @Body() dto: CapabilitiesDto): Promise<DriverCapabilitiesResponseDto> {
    return this.preferences.updateCapabilities(driver, dto);
  }

  @Post('registration/step/:step')
  @HttpCode(200)
  step(
    @CurrentDriver() driver: Driver,
    @Param('step', new StepPipe()) step: number,
    @Body() body: unknown,
    @Headers('x-request-id') requestId?: string,
  ): Promise<RegistrationStateResponseDto> {
    return this.registration.submitStep(driver, step, body, requestId);
  }
}
