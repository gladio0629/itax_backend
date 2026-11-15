import { Body, Controller, Get, Headers, HttpCode, Param, Post, Query } from '@nestjs/common';
import { Actor } from '../audit/audit.service';
import { CurrentActor, Perm } from '../auth/admin.guard';
import type { Page } from '../common/pagination';
import { UuidParamPipe } from '../common/pipes';
import { LedgerEntryView, WalletPort } from '../ports/ports';
import { ApprovalRejectDto, CorrectionCreateDto, LedgerQuery } from './wallet.dto';
import { CorrectionView, WalletService } from './wallet.service';

@Controller('admin/v1/wallet')
export class WalletController {
  constructor(private readonly wallet: WalletPort, private readonly svc: WalletService) {}

  @Get('ledger') @Perm('wallet:read')
  ledger(@Query() q: LedgerQuery): Promise<Page<LedgerEntryView>> {
    return this.wallet.ledger({ walletId: q.wallet_id, driverId: q.driver_id }, q);
  }

  @Post('correction') @Perm('wallet:write') @HttpCode(202)
  create(
    @CurrentActor() actor: Actor,
    @Body() dto: CorrectionCreateDto,
    @Headers('idempotency-key') key?: string,
  ): Promise<CorrectionView> {
    return this.svc.create(actor, dto, key);
  }

  @Post('correction/:id/approve') @Perm('wallet:write') @HttpCode(200)
  approve(@CurrentActor() actor: Actor, @Param('id', new UuidParamPipe()) id: string): Promise<CorrectionView> {
    return this.svc.approve(actor, id);
  }

  @Post('correction/:id/reject') @Perm('wallet:write') @HttpCode(200)
  reject(@CurrentActor() actor: Actor, @Param('id', new UuidParamPipe()) id: string, @Body() dto: ApprovalRejectDto): Promise<CorrectionView> {
    return this.svc.reject(actor, id, dto.reason);
  }
}
