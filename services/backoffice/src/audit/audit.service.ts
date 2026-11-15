import { Injectable } from '@nestjs/common';
import { EntityManager } from 'typeorm';
import { v4 as uuidv4 } from 'uuid';
import { AuditLog } from '../entities/audit-log.entity';
import type { StaffRole } from '../roles/roles.catalog';

export interface Actor {
  /** null — tizim (xodim ishtirok etmagan amal) */
  id: string | null;
  role: StaffRole | 'system';
  requestId?: string;
}

export const SYSTEM_ACTOR: Actor = { id: null, role: 'system' };

export interface AuditEntry {
  action: string;
  entityType: string;
  entityId: string;
  before?: Record<string, unknown> | null;
  after?: Record<string, unknown> | null;
}

/**
 * Audit yozuvi. Faqat INSERT: bu servisda update/delete metodi yo'q, DB trigger'i ham taqiqlaydi.
 * `em` — biznes o'zgarishi bilan bir tranzaksiya (ikkalasi birga saqlanadi yoki birga bekor bo'ladi).
 */
@Injectable()
export class AuditService {
  async record(em: EntityManager, actor: Actor, e: AuditEntry): Promise<void> {
    await em.getRepository(AuditLog).insert({
      id: uuidv4(),
      actorId: actor.id,
      actorRole: actor.role,
      action: e.action,
      entityType: e.entityType,
      entityId: e.entityId,
      before: (e.before ?? null) as never,
      after: (e.after ?? null) as never,
      requestId: actor.requestId ?? null,
      createdAt: new Date(),
    });
  }
}
