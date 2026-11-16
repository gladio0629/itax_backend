import { Logger } from '@nestjs/common';
import { maskEmail, maskPhone, maskPii, PiiMaskingLogger } from './pii';

describe('PII maskalash', () => {
  it('telefon: 998**___**67', () => {
    expect(maskPhone('+998901234567')).toBe('998**___**67');
    expect(maskPhone('998901234567')).toBe('998**___**67');
    expect(maskPhone(null)).toBeNull();
  });

  it('email: s***@***.com', () => {
    expect(maskEmail('sardor@example.com')).toBe('s***@***.com');
    expect(maskEmail('a.b+tag@mail.co.uz')).toBe('a***@***.uz');
    expect(maskEmail(null)).toBeNull();
  });

  it('matn ichidagi telefon va email maskalanadi', () => {
    const out = maskPii('user phone=+998901234567, alt 998 90 123 45 67, mail sardor@example.com; intl +79161234567');
    expect(out).toBe('user phone=998**___**67, alt 998**___**67, mail s***@***.com; intl 791**___**67');
  });

  it('DB xatosi (unique violation) dagi telefon maskalanadi', () => {
    expect(maskPii('Key (phone)=(+998901234567) already exists.')).toBe('Key (phone)=(998**___**67) already exists.');
  });

  it('UUID, vaqt, port, request_id kabi raqamlar tegilmaydi', () => {
    const s = [
      'driver=00000000-0000-0000-0000-998901234567',
      'id=a1b2c3d4-1111-2222-3333-444455556666',
      'at 2026-11-16T10:00:00+05:00',
      'listening :8017 took 998901234ms',
      'ts=1731450000000',
    ].join(' | ');
    expect(maskPii(s)).toBe(s);
  });
});

describe('PiiMaskingLogger — haqiqiy chiqish', () => {
  let out: string[];
  let spies: jest.SpyInstance[];

  beforeEach(() => {
    out = [];
    const capture = (chunk: unknown) => { out.push(String(chunk)); return true; };
    spies = [
      jest.spyOn(process.stdout, 'write').mockImplementation(capture as never),
      jest.spyOn(process.stderr, 'write').mockImplementation(capture as never),
    ];
  });

  afterEach(() => {
    spies.forEach((s) => s.mockRestore());
    Logger.overrideLogger(true as never);
  });

  it('log/warn/error xabari va stack trace maskalanadi', () => {
    const logger = new PiiMaskingLogger();
    logger.log('kirdi +998901234567 sardor@example.com', 'Test');
    logger.warn({ phone: '+998901234567' }, 'Test');
    logger.error('xato', 'Error: Key (phone)=(+998901234567)\n    at f (x.ts:1:1)', 'Test');

    const all = out.join('');
    expect(all).not.toContain('901234567');
    expect(all).not.toContain('sardor@');
    expect(all).toContain('998**___**67');
    expect(all).toContain('s***@***.com');
  });

  it('`new Logger(ctx)` chaqiruvlari ham (main.ts dagi kabi o\'rnatilganda) maskalanadi', () => {
    Logger.overrideLogger(new PiiMaskingLogger());
    new Logger('Svc').warn('sinxronlanmadi: +998901234567');
    const all = out.join('');
    expect(all).toContain('998**___**67');
    expect(all).not.toContain('+998901234567');
  });
});
