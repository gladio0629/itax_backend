/** Hodisani brokerga yuboradi. NATS JetStream implementatsiyasi va test uchun fake bor. */
export abstract class EventPublisher {
  /** `msgId` — JetStream deduplikatsiyasi uchun (event_id). Xatoda throw qiladi. */
  abstract publish(subject: string, payload: Record<string, unknown>, msgId: string): Promise<void>;
}
