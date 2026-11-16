import type { NextFunction, Request, Response } from 'express';

/** F-08: barcha javoblarga (xato javoblari ham) qo'yiladigan xavfsizlik header'lari. */
export const SECURITY_HEADERS: Readonly<Record<string, string>> = {
  'Content-Security-Policy': "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'",
  'X-Frame-Options': 'DENY',
  'X-Content-Type-Options': 'nosniff',
  'Strict-Transport-Security': 'max-age=31536000; includeSubDomains',
};

export function securityHeaders(_req: Request, res: Response, next: NextFunction): void {
  for (const [k, v] of Object.entries(SECURITY_HEADERS)) res.setHeader(k, v);
  next();
}
