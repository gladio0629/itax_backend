import { Global, Module } from '@nestjs/common';
import { OperatorModule } from '../operator/operator.module';
import { EventPublisher } from './event-publisher';
import { InboundEvents } from './inbound-events';
import { NatsService } from './nats.service';
import { OutboxDispatcher } from './outbox.dispatcher';

@Global()
@Module({
  imports: [OperatorModule],
  providers: [InboundEvents, NatsService, { provide: EventPublisher, useExisting: NatsService }, OutboxDispatcher],
  exports: [EventPublisher],
})
export class EventsModule {}
