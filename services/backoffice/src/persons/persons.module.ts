import { Module } from '@nestjs/common';
import { DriversAdminController, PassengersAdminController } from './persons.controller';
import { PersonsService } from './persons.service';

@Module({ controllers: [DriversAdminController, PassengersAdminController], providers: [PersonsService] })
export class PersonsModule {}
