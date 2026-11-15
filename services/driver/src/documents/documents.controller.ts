import { Body, Controller, Headers, HttpCode, Param, Post, Put, Query, Req, UseGuards } from '@nestjs/common';
import type { Request } from 'express';
import { CurrentDriver, DriverAuthGuard } from '../auth/auth.guard';
import { UuidParamPipe } from '../common/pipes';
import { UploadUrlRequestDto } from '../drivers/dto/request.dto';
import { UploadUrlResponseDto } from '../drivers/dto/response.dto';
import type { Driver } from '../drivers/entities/driver.entity';
import { DocumentsService } from './documents.service';

@Controller('api/v1/drivers/documents')
export class DocumentsController {
  constructor(private readonly documents: DocumentsService) {}

  @Post('upload-url')
  @UseGuards(DriverAuthGuard)
  createUploadUrl(@CurrentDriver() driver: Driver, @Body() dto: UploadUrlRequestDto): Promise<UploadUrlResponseDto> {
    return this.documents.createUploadUrl(driver, dto);
  }

  /** Imzolangan URL (Bearer kerak emas — S3 presigned URL kabi). Body — xom bayt (express.raw, 10 MB). */
  @Put(':id/content')
  @HttpCode(204)
  async upload(
    @Param('id', new UuidParamPipe()) id: string,
    @Query('expires') expires: string,
    @Query('signature') signature: string,
    @Headers('content-type') contentType: string | undefined,
    @Req() req: Request,
  ): Promise<void> {
    await this.documents.upload(id, expires, signature, contentType, req.body);
  }
}
