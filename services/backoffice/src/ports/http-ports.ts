import { Logger } from '@nestjs/common';
import { AppError, Errors } from '../common/errors';
import { isUuid } from '../common/pipes';
import { InternalHttpClient } from './internal-http';
import { DriverLookupPort, IdentityStaff, IdentityStaffPort, UserLookupPort } from './ports';

/** Identity: POST /internal/identity/staff  ->  201 {id, phone, role} | 409 {error:{key}} | 422 | 5xx */
export class HttpIdentityStaffPort extends IdentityStaffPort {
  private readonly log = new Logger(HttpIdentityStaffPort.name);

  constructor(private readonly http: InternalHttpClient) { super(); }

  async create(s: { phone: string; email: string | null; role: string; name: string }): Promise<{ id: string }> {
    const r = await this.http.request('POST', '/internal/identity/staff', {
      phone: s.phone, email: s.email, role: s.role, name: s.name,
    });
    if (r.status === 201 && isUuid(r.json?.id)) return { id: r.json.id.toLowerCase() };
    if (r.status === 409) {
      const key = r.json?.error?.key;
      if (key === 'STAFF_PHONE_TAKEN') throw Errors.staffPhoneTaken();
      if (key === 'STAFF_EMAIL_TAKEN') throw Errors.staffEmailTaken();
    }
    if (r.status === 422 && r.json?.error?.key === 'VALIDATION_ERROR') {
      throw new AppError(422, 'VALIDATION_ERROR', r.json.error.params ?? {});
    }
    // 401/403 (token mos emas), 5xx, kutilmagan javob — xodim yaratilmaydi
    this.log.error(`identity xodim yaratishni rad etdi: HTTP ${r.status} ${JSON.stringify(r.json?.error ?? null)}`);
    throw Errors.identityUnavailable(`http_${r.status}`);
  }

  /**
   * GET /internal/identity/staff/{id} -> 200 {id, phone, email, role, name, active} | 404 STAFF_NOT_FOUND | 5xx.
   * Faqat STAFF_NOT_FOUND kalitli 404 — "yo'q" (null). Kalitsiz 404/405 (eski identity), 5xx, timeout,
   * shakli noto'g'ri javob — IDENTITY_UNAVAILABLE: identity'da xodim bormi-yo'qmi aniq emas.
   */
  async get(id: string): Promise<IdentityStaff | null> {
    const r = await this.http.request('GET', `/internal/identity/staff/${encodeURIComponent(id)}`);
    if (r.status === 404 && r.json?.error?.key === 'STAFF_NOT_FOUND') return null;
    const b = r.json;
    const strOrNull = (v: unknown) => v === null || typeof v === 'string';
    if (r.status === 200 && isUuid(b?.id) && b.id.toLowerCase() === id.toLowerCase() && typeof b.phone === 'string'
      && strOrNull(b.email) && typeof b.role === 'string' && typeof b.name === 'string' && typeof b.active === 'boolean') {
      return { id: b.id.toLowerCase(), email: b.email, phone: b.phone, fullName: b.name, role: b.role, active: b.active };
    }
    this.log.error(`identity xodimni qaytarmadi (${id}): HTTP ${r.status} ${JSON.stringify(r.json?.error ?? null)}`);
    throw Errors.identityUnavailable(r.status === 200 ? 'invalid_response' : `http_${r.status}`);
  }

  /**
   * PUT /internal/identity/staff/{id} -> 200 | 404 STAFF_NOT_FOUND | 409 | 422 | 5xx.
   * Har doim to'liq joriy holat yuboriladi (identity qisman yangilashni ham qabul qiladi). `active: false` —
   * bloklangan/arxivlangan xodim identity orqali ham kira olmaydi. Xato bo'lsa backoffice tranzaksiyasi
   * bekor bo'ladi (ikki tomon bir-biridan farq qilmasin).
   */
  async update(staff: IdentityStaff): Promise<void> {
    const r = await this.http.request('PUT', `/internal/identity/staff/${encodeURIComponent(staff.id)}`, {
      phone: staff.phone, email: staff.email, role: staff.role, name: staff.fullName, active: staff.active,
    });
    if (r.status === 200) return;
    if (r.status === 409) {
      const key = r.json?.error?.key;
      if (key === 'STAFF_PHONE_TAKEN') throw Errors.staffPhoneTaken();
      if (key === 'STAFF_EMAIL_TAKEN') throw Errors.staffEmailTaken();
    }
    if (r.status === 422 && r.json?.error?.key === 'VALIDATION_ERROR') {
      throw new AppError(422, 'VALIDATION_ERROR', r.json.error.params ?? {});
    }
    // 404 — xodim identity'da yo'q (sinxron emas): yozuvni o'zgartirmaymiz, operator aralashuvi kerak
    this.log.error(`identity xodim yangilashni rad etdi (${staff.id}): HTTP ${r.status} ${JSON.stringify(r.json?.error ?? null)}`);
    throw Errors.identityUnavailable(r.status === 404 ? 'staff_not_in_identity' : `http_${r.status}`);
  }
}

async function getPhone(http: InternalHttpClient, path: string, id: string): Promise<{ id: string; phone: string | null } | null> {
  const r = await http.request('GET', path);
  if (r.status === 404) return null;
  if (r.status !== 200) throw Errors.identityUnavailable(`http_${r.status}`);
  // Kutilgan shakl (shartnoma hali yo'q): { id, phone, ... }
  return { id, phone: typeof r.json?.phone === 'string' ? r.json.phone : null };
}

/** Driver: GET /internal/driver/drivers/{id} */
export class HttpDriverLookupPort extends DriverLookupPort {
  constructor(private readonly http: InternalHttpClient) { super(); }
  getDriver(id: string) { return getPhone(this.http, `/internal/driver/drivers/${encodeURIComponent(id)}`, id); }
}

/** Identity: GET /internal/identity/users/{id} */
export class HttpUserLookupPort extends UserLookupPort {
  constructor(private readonly http: InternalHttpClient) { super(); }
  getUser(id: string) { return getPhone(this.http, `/internal/identity/users/${encodeURIComponent(id)}`, id); }
}
