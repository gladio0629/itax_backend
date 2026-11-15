import { AuditLog } from './audit-log.entity';
import { Complaint, ComplaintNote } from './complaint.entity';
import { Correction } from './correction.entity';
import { OutboxEvent } from './outbox.entity';
import { QueueItem } from './queue-item.entity';
import { Staff } from './staff.entity';
import { Tariff } from './tariff.entity';

export const ENTITIES = [Staff, QueueItem, Tariff, Correction, AuditLog, OutboxEvent, Complaint, ComplaintNote];
