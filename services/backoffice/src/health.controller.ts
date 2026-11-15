import { Controller, Get } from '@nestjs/common';
import { Public } from './auth/admin.guard';

@Controller()
export class HealthController {
  @Get('healthz') @Public()
  health(): string {
    return 'ok';
  }
}
