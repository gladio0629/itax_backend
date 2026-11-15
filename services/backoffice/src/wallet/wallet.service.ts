import { Injectable } from '@nestjs/common';
import { createHash } from 'crypto';
import { DataSource } from 'typeorm';
import { v4 as uuidv4 } from 'uuid';
import { Actor, AuditService } from '../audit/audit.service';
import { Errors } from '../common/errors';
import { isUuid } from '../common/pipes';
import { Correction } from '../entities/correction.entity';
import { WalletPort } from '../ports/ports';
import { CorrectionCreateDto } from './wallet.dto';

export interface CorrectionView {
  id: string; wallet_id: string; amount: number; reason: string; approval_status: string;
  created_by: string; approved_by: string | null; reject_reason: string | null; created_at: string;
}

const toView = (c: Correction): CorrectionView => ({
  id: c.id, wallet_id: c.walletId, amount: c.amount, reason: c.reason, approval_status: c.approvalStatus,
  created_by: c.createdBy, approved_by: c.approvedBy, reject_reason: c.rejectReason, created_at: c.createdAt.toISOString(),
});
const snap = (c: Correction): Record<string, unknown> => ({
  id: c.id, wallet_id: c.walletId, amount: c.amount, approval_status: c.approvalStatus,
  created_by: c.createdBy, approved_by: c.approvedBy, reject_reason: c.rejectReason,
});

/** Hamyon tuzatishlari — ikki kishi tasdig'i (yaratgan xodim o'zi tasdiqlay/rad eta olmaydi). */
@Injectable()
export class WalletService {
  constructor(
    private readonly ds: DataSource,
    private readonly audit: AuditService,
    private readonly wallet: WalletPort,
  ) {}

  async create(actor: Actor, dto: CorrectionCreateDto, idempotencyKey?: string): Promise<CorrectionView> {
    if (idempotencyKey !== undefined && !isUuid(idempotencyKey)) throw Errors.validation({ 'idempotency-key': 'isUuid' });
    const hash = createHash('sha256').update(JSON.stringify([dto.wallet_id, dto.amount, dto.reason])).digest('hex');

    const repo = this.ds.getRepository(Correction);
    if (idempotencyKey) {
      const prev = await repo.findOne({ where: { createdBy: actor.id!, idempotencyKey } });
      if (prev) {
        if (prev.requestHash !== hash) throw Errors.idempotencyConflict();
        return toView(prev); // takroriy so'rov: asl javob
      }
    }
    if (!(await this.wallet.walletExists(dto.wallet_id))) throw Errors.validation({ wallet_id: 'not_found' });

    const c = repo.create({
      id: uuidv4(), walletId: dto.wallet_id, amount: dto.amount, reason: dto.reason, approvalStatus: 'pending_approval',
      createdBy: actor.id!, approvedBy: null, rejectReason: null, idempotencyKey: idempotencyKey ?? null,
      requestHash: idempotencyKey ? hash : null, createdAt: new Date(), decidedAt: null,
    });
    try {
      await this.ds.transaction(async (em) => {
        await em.getRepository(Correction).insert(c);
        await this.audit.record(em, actor, {
          action: 'wallet.correction.create', entityType: 'correction', entityId: c.id, before: null, after: snap(c),
        });
      });
    } catch (e) {
      // parallel bir xil kalit: ikkinchisi birinchisining natijasini oladi
      if (idempotencyKey && /unique|duplicate/i.test((e as Error).message)) {
        const prev = await repo.findOne({ where: { createdBy: actor.id!, idempotencyKey } });
        if (prev) return toView(prev);
      }
      throw e;
    }
    return toView(c);
  }

  async approve(actor: Actor, id: string): Promise<CorrectionView> {
    // Daftarga yozish (wallet servisi — port) o'sha tranzaksiya ichida: port xato bersa tuzatish
    // pending_approval holatiga qaytadi va qayta tasdiqlash mumkin. Port correction id bo'yicha idempotent,
    // shuning uchun commit xato bersa ham qayta urinish ikki marta yozmaydi.
    return toView(await this.decide(actor, id, 'approved', null, (c) =>
      this.wallet.applyCorrection({ correctionId: c.id, walletId: c.walletId, amount: c.amount })));
  }

  async reject(actor: Actor, id: string, reason: string): Promise<CorrectionView> {
    return toView(await this.decide(actor, id, 'rejected', reason));
  }

  /**
   * Bitta UPDATE: faqat pending_approval va yaratuvchi boshqa xodim bo'lsa o'tadi.
   * Parallel approve/reject bo'lsa faqat bittasi yutadi.
   */
  private async decide(
    actor: Actor, id: string, to: 'approved' | 'rejected', reason: string | null,
    sideEffect?: (c: Correction) => Promise<void>,
  ): Promise<Correction> {
    return this.ds.transaction(async (em) => {
      const res = await em.getRepository(Correction).createQueryBuilder().update()
        .set({ approvalStatus: to, approvedBy: actor.id!, rejectReason: reason, decidedAt: new Date() })
        .where("id = :id AND approval_status = 'pending_approval' AND created_by <> :me", { id, me: actor.id })
        .execute();
      const c = await em.getRepository(Correction).findOne({ where: { id } });
      if (!c) throw Errors.notFound(id);
      if ((res.affected ?? 0) !== 1) {
        if (c.createdBy === actor.id) throw Errors.forbidden('self_approval');
        throw Errors.forbidden('not_pending');
      }
      if (sideEffect) await sideEffect(c);
      await this.audit.record(em, actor, {
        action: to === 'approved' ? 'wallet.correction.approve' : 'wallet.correction.reject',
        entityType: 'correction', entityId: id,
        before: { approval_status: 'pending_approval' }, after: snap(c),
      });
      return c;
    });
  }
}
