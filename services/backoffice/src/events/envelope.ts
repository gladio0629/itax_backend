import { v4 as uuidv4 } from 'uuid';

/** shared/events/*.json dagi umumiy envelope */
export interface Envelope<P> {
  event: string;
  version: 1;
  event_id: string;
  occurred_at: string;
  trace_id?: string;
  payload: P;
}

export const SUBJECT_DRIVER_SUBMITTED = 'izgo.driver.submitted.v1';
export const SUBJECT_DRIVER_APPROVED = 'izgo.driver.approved.v1';
export const SUBJECT_USER_CREATED = 'izgo.user.created.v1';
/** identity -> backoffice audit (shared/events/staff.logged_in.json, staff.logged_out.json) */
export const SUBJECT_STAFF_LOGGED_IN = 'izgo.user.staff_logged_in.v1';
export const SUBJECT_STAFF_LOGGED_OUT = 'izgo.user.staff_logged_out.v1';

export interface OutboxDraft {
  id: string;
  subject: string;
  payload: Record<string, unknown>;
}

function envelope<P>(event: string, payload: P, traceId?: string, now = new Date()): Envelope<P> & { event_id: string } {
  const e: Envelope<P> = {
    event,
    version: 1,
    event_id: uuidv4(),
    occurred_at: now.toISOString(),
    payload,
  };
  if (traceId) e.trace_id = traceId;
  return e;
}

function draft(subject: string, env: Envelope<unknown>): OutboxDraft {
  return { id: env.event_id, subject, payload: env as unknown as Record<string, unknown> };
}

export function driverApprovedEvent(
  p: { driverId: string; approvedBy: string; approvedAt: Date },
  traceId?: string,
): OutboxDraft {
  return draft(
    SUBJECT_DRIVER_APPROVED,
    envelope(
      'driver.approved',
      { driver_id: p.driverId, approved_by: p.approvedBy, approved_at: p.approvedAt.toISOString() },
      traceId,
      p.approvedAt,
    ),
  );
}
