import { Injectable } from '@nestjs/common';
import { ApprovalService } from '../approval/approval.service';
import { Errors } from '../common/errors';
import { validateDto } from '../common/validation';
import { driverSubmittedEvent } from '../events/envelope';
import { assertNotBlocked } from './drivers.service';
import { Step1Dto, Step2Dto, Step3Dto, Step4Dto } from './dto/request.dto';
import { RegistrationStateResponseDto } from './dto/response.dto';
import type { Driver } from './entities/driver.entity';
import { DriverStore } from './store/driver.store';

const REQUIRED_DOCUMENT_KINDS = ['license_front', 'selfie'];
const MIN_AGE = 18;

@Injectable()
export class RegistrationService {
  constructor(
    private readonly store: DriverStore,
    private readonly approval: ApprovalService,
  ) {}

  async getState(driver: Driver): Promise<RegistrationStateResponseDto> {
    const fresh = (await this.store.findById(driver.id)) ?? driver;
    const steps = await this.store.getSteps(fresh.id);
    const completed = steps.map((s) => s.step).filter((s) => s >= 1 && s <= 3);
    // 4-qadam "bajarilgan" = ariza yuborilgan
    if (fresh.state === 'pending_review' || fresh.state === 'active') completed.push(4);
    completed.sort((a, b) => a - b);
    const next = [1, 2, 3, 4].find((s) => !completed.includes(s)) ?? 4;
    return { current_step: next, completed_steps: completed, state: fresh.state };
  }

  /** Har qadam alohida saqlanadi; haydovchi istalgan qadamdan davom eta oladi. */
  async submitStep(driver: Driver, step: number, body: unknown, traceId?: string): Promise<RegistrationStateResponseDto> {
    assertNotBlocked(driver);
    const current = (await this.store.findById(driver.id)) ?? driver;
    assertNotBlocked(current);

    if (step === 4) return this.submit(current, body, traceId);

    // yuborilgan/faol arizaning 1–3 qadamlari o'zgarmaydi (o'zgartirish — qayta tekshiruv talab qiladi)
    if (current.state === 'pending_review' || current.state === 'active') throw Errors.alreadySubmitted();

    switch (step) {
      case 1: await this.step1(current, await validateDto(Step1Dto, body)); break;
      case 2: await this.step2(current, await validateDto(Step2Dto, body)); break;
      case 3: await this.step3(current, await validateDto(Step3Dto, body)); break;
      default: throw Errors.validation({ step: 'invalid' });
    }
    return this.getState(current);
  }

  private async step1(d: Driver, dto: Step1Dto): Promise<void> {
    checkBirthDate(dto.birth_date);
    await this.store.updateProfile(d.id, {
      firstName: dto.first_name,
      lastName: dto.last_name,
      birthDate: dto.birth_date,
      city: dto.city,
    });
    await this.store.saveStep(d.id, 1, { ...dto });
  }

  private async step2(d: Driver, dto: Step2Dto): Promise<void> {
    const c = dto.car;
    await this.store.upsertCar({
      driverId: d.id,
      brand: c.brand,
      model: c.model,
      year: c.year,
      color: c.color,
      plateNumber: c.plate_number,
      carClass: c.car_class,
      seats: c.seats,
    });
    await this.store.saveStep(d.id, 2, { car: { ...c } });
  }

  private async step3(d: Driver, dto: Step3Dto): Promise<void> {
    const kinds = dto.documents.map((x) => x.kind);
    const ids = dto.documents.map((x) => x.document_id);
    if (new Set(kinds).size !== kinds.length) throw Errors.validation({ documents: 'duplicate_kind' });
    if (new Set(ids).size !== ids.length) throw Errors.validation({ documents: 'duplicate_document' });
    for (const k of REQUIRED_DOCUMENT_KINDS) {
      if (!kinds.includes(k as never)) throw Errors.validation({ [`documents.${k}`]: 'required' });
    }

    // F-10: har bir hujjat shu haydovchiniki, yuklangan va turi mos bo'lishi shart.
    // Begona yoki mavjud bo'lmagan ID bir xil javob beradi (mavjudlik oshkor bo'lmaydi).
    const found = new Map((await this.store.findDocuments(d.id, ids)).map((x) => [x.id, x]));
    const fields: Record<string, string> = {};
    dto.documents.forEach((ref, i) => {
      const doc = found.get(ref.document_id);
      if (!doc) fields[`documents.${i}.document_id`] = 'not_found';
      else if (doc.kind !== ref.kind) fields[`documents.${i}.kind`] = 'mismatch';
      else if (doc.status !== 'uploaded') fields[`documents.${i}.document_id`] = 'not_uploaded';
    });
    if (Object.keys(fields).length) throw Errors.validation(fields);

    await this.store.saveStep(d.id, 3, { documents: dto.documents.map((x) => ({ ...x })) });
  }

  private async submit(d: Driver, body: unknown, traceId?: string): Promise<RegistrationStateResponseDto> {
    await validateDto(Step4Dto, body);

    // Takroriy so'rov: allaqachon yuborilgan — hodisa qayta chiqmaydi
    if (d.state === 'pending_review' || d.state === 'active') return this.getState(d);

    const steps = await this.store.getSteps(d.id);
    const missing: Record<string, string> = {};
    for (const n of [1, 2, 3]) {
      if (!steps.some((s) => s.step === n)) missing[`step_${n}`] = 'incomplete';
    }
    if (Object.keys(missing).length) throw Errors.validation(missing);

    const docs = (steps.find((s) => s.step === 3)!.data as { documents: { document_id: string }[] }).documents;
    const submittedAt = new Date();
    const moved = await this.store.transition({
      id: d.id,
      from: ['draft', 'rejected'],
      to: 'pending_review',
      reason: null,
      submittedAt,
      outbox: driverSubmittedEvent(
        { driverId: d.id, userId: d.userId, documentIds: docs.map((x) => x.document_id), submittedAt },
        traceId,
      ),
    });
    // moved=false — parallel so'rov oldin o'tkazib qo'ygan; hodisa faqat bir marta chiqadi
    if (moved) this.approval.schedule(d.id);
    return this.getState(d);
  }
}

/** YYYY-MM-DD haqiqiy sana, kelajakda emas va haydovchi kamida 18 yoshda bo'lishi kerak. */
export function checkBirthDate(value: string, now: Date = new Date()): void {
  const [y, m, day] = value.split('-').map(Number);
  const dt = new Date(Date.UTC(y, m - 1, day));
  if (dt.getUTCFullYear() !== y || dt.getUTCMonth() !== m - 1 || dt.getUTCDate() !== day) {
    throw Errors.validation({ birth_date: 'invalid_date' });
  }
  const cutoff = new Date(Date.UTC(now.getUTCFullYear() - MIN_AGE, now.getUTCMonth(), now.getUTCDate()));
  if (dt > now) throw Errors.validation({ birth_date: 'in_future' });
  if (dt > cutoff) throw Errors.validation({ birth_date: 'too_young' });
  if (y < 1900) throw Errors.validation({ birth_date: 'invalid_date' });
}
