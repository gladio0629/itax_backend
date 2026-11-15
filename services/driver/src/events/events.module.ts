import { Global, Module } from '@nestjs/common';
import { OutboxDispatcher } from './outbox.dispatcher';
import { InboundEvents } from './inbound-events';
import { NatsService } from './nats.service';
import { EventPublisher } from './event-publisher';
import { MatchingSync } from '../matching/matching-sync.service';

@Global()
@Module({
  providers: [InboundEvents, NatsService, { provide: EventPublisher, useExisting: NatsService }, OutboxDispatcher, MatchingSync],
  exports: [EventPublisher, InboundEvents, MatchingSync],
})
export class EventsModule {}
