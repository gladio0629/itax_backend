import { ConsoleLogger } from '@nestjs/common';

/**
 * PII maskalash (logda va operator navbatida). Format:
 *   telefon: +998901234567 -> 998**___**67
 *   email:   sardor@example.com -> s***@***.com
 */
export function maskPhone(phone: string | null): string | null {
  if (!phone) return null;
  const d = phone.replace(/\D/g, '');
  if (d.length < 5) return '***';
  return `${d.slice(0, 3)}**___**${d.slice(-2)}`;
}

export function maskEmail(email: string | null): string | null {
  if (!email) return null;
  const at = email.lastIndexOf('@');
  if (at < 1) return '***';
  const domain = email.slice(at + 1);
  const dot = domain.lastIndexOf('.');
  const tld = dot >= 0 ? domain.slice(dot + 1) : '';
  return `${email[0]}***@***${tld ? `.${tld}` : ''}`;
}

const EMAIL_RE = /[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}/g;
// O'zbek raqami (+998 ixtiyoriy, bo'shliq/chiziqcha bilan ham) va boshqa xalqaro (+ bilan, 10–15 raqam).
// Chegaralar: UUID/ID/vaqt belgisi ichidagi raqamlar yutilmasin (oldin/keyin harf, raqam yoki "-" bo'lmasin).
const PHONE_RE = /(?<![\w-])(?:\+?998(?:[\s-]?\d){9}|\+\d{10,15})(?![\w-])/g;

/** Matndagi barcha telefon va email'larni maskalaydi. */
export function maskPii(text: string): string {
  return text.replace(EMAIL_RE, (m) => maskEmail(m) as string).replace(PHONE_RE, (m) => maskPhone(m) as string);
}

/**
 * Nest logger: har bir xabar va stack trace chiqishdan oldin maskalanadi. main.ts da `logger` sifatida
 * beriladi — mavjud `new Logger(ctx)` chaqiruvlari shu orqali yozadi (chaqiruv joylari o'zgarmaydi).
 */
export class PiiMaskingLogger extends ConsoleLogger {
  protected formatMessage(...args: Parameters<ConsoleLogger['formatMessage']>): string {
    return maskPii(super.formatMessage(...args));
  }

  protected printStackTrace(stack: string): void {
    super.printStackTrace(stack ? maskPii(stack) : stack);
  }
}
