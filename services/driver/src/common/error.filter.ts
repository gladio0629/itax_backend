import { ArgumentsHost, Catch, ExceptionFilter, HttpException, Logger } from '@nestjs/common';
import type { Response } from 'express';
import { randomUUID } from 'crypto';

/** Barcha xatolarni {"error":{"key","params"}} ko'rinishiga keltiradi. */
@Catch()
export class AppExceptionFilter implements ExceptionFilter {
  private readonly log = new Logger('Exceptions');

  catch(exception: unknown, host: ArgumentsHost): void {
    const res = host.switchToHttp().getResponse<Response>();
    const req = host.switchToHttp().getRequest<{ headers: Record<string, string | undefined> }>();

    if (exception instanceof HttpException) {
      const body = exception.getResponse() as { error?: { key?: string } } | string;
      if (typeof body === 'object' && body?.error?.key) {
        res.status(exception.getStatus()).json(body);
        return;
      }
      let status = exception.getStatus();
      // body-parser noto'g'ri JSON da 400 beradi; OpenAPI bo'yicha so'rov xatosi — 422 VALIDATION_ERROR
      if (status === 400) status = 422;
      res.status(status).json({ error: { key: statusKey(status), params: {} } });
      return;
    }

    // body-parser xatolari (hajm oshib ketgan, noto'g'ri JSON) — `status`/`type` bilan keladi
    const e = exception as { status?: number; type?: string; limit?: number };
    if (e?.type === 'entity.too.large') {
      res.status(413).json({ error: { key: 'FILE_TOO_LARGE', params: { max_bytes: e.limit ?? 0 } } });
      return;
    }
    if (e?.type === 'entity.parse.failed') {
      res.status(422).json({ error: { key: 'VALIDATION_ERROR', params: { fields: { body: 'invalid_json' } } } });
      return;
    }

    const requestId = req.headers['x-request-id'] ?? randomUUID();
    this.log.error(`[${requestId}] ${exception instanceof Error ? exception.stack : String(exception)}`);
    res.status(500).json({ error: { key: 'INTERNAL_ERROR', params: { request_id: requestId } } });
  }
}

function statusKey(status: number): string {
  switch (status) {
    case 401: return 'AUTH_TOKEN_EXPIRED';
    case 403: return 'FORBIDDEN';
    case 404: return 'NOT_FOUND';
    case 413: return 'FILE_TOO_LARGE';
    case 400:
    case 415: return 'VALIDATION_ERROR';
    case 422: return 'VALIDATION_ERROR';
    default: return status >= 500 ? 'INTERNAL_ERROR' : 'VALIDATION_ERROR';
  }
}
