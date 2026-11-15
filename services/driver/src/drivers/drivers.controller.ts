import { Body, Controller, Get, Headers, HttpCode, Param, Post, Put, UseGuards } from '@nestjs/common';
import { CurrentDriver, DriverAuthGuard } from '../auth/auth.guard';
import { StepPipe } from '../common/pipes';
import { DriversService } from './drivers.service';
import { UpdateProfileDto } from './dto/request.dto';
import { DriverProfileResponseDto, DriverStatusResponseDto, RegistrationStateResponseDto } from './dto/response.dto';
import type { Driver } from './entities/driver.entity';
import { RegistrationService } from './registration.service';

@Controller('api/v1/drivers')
@UseGuards(DriverAuthGuard)
export class DriversController {
  constructor(
    private readonly drivers: DriversService,
    private readonly registration: RegistrationService,
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
