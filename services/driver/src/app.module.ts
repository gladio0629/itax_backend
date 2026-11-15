import { DynamicModule, Global, Module } from '@nestjs/common';
import { TypeOrmModule } from '@nestjs/typeorm';
import { DriverAuthGuard, JWT_VERIFIER } from './auth/auth.guard';
import { InternalTokenGuard } from './auth/internal-token.guard';
import { JwtVerifier } from './auth/jwt.verifier';
import { APP_CONFIG, AppConfig, loadConfig } from './config/configuration';
import { DriversModule } from './drivers/drivers.module';
import { DriverCapabilities } from './drivers/entities/capabilities.entity';
import { Car } from './drivers/entities/car.entity';
import { DriverDocument } from './drivers/entities/document.entity';
import { Driver } from './drivers/entities/driver.entity';
import { OutboxEvent } from './drivers/entities/outbox.entity';
import { RegistrationStep } from './drivers/entities/registration-step.entity';
import { DriverRoute } from './drivers/entities/route.entity';
import { DriverStore } from './drivers/store/driver.store';
import { TypeOrmDriverStore } from './drivers/store/typeorm-driver.store';
import { EventsModule } from './events/events.module';
import { HealthController } from './health.controller';
import { Init1731360000000 } from './migrations/1731360000000-init';
import { RoutesCapabilities1794756143798 } from './migrations/1794756143798-routes-capabilities';

/** Konfiguratsiya, JWT tekshiruvchi va auth guard — barcha modullarga umumiy. */
@Global()
@Module({})
export class CoreModule {
  static register(cfg: AppConfig, extra: DynamicModule[] = []): DynamicModule {
    return {
      module: CoreModule,
      imports: extra,
      providers: [
        { provide: APP_CONFIG, useValue: cfg },
        { provide: JWT_VERIFIER, useValue: new JwtVerifier(cfg.jwt.publicKeyPem, cfg.jwt.issuer, 'driver') },
        DriverAuthGuard,
        InternalTokenGuard,
      ],
      exports: [APP_CONFIG, JWT_VERIFIER, DriverAuthGuard, InternalTokenGuard],
    };
  }
}

@Global()
@Module({
  imports: [
    TypeOrmModule.forRootAsync({
      inject: [APP_CONFIG],
      useFactory: (cfg: AppConfig) => ({
        type: 'postgres' as const,
        host: cfg.db.host,
        port: cfg.db.port,
        username: cfg.db.user,
        password: cfg.db.password,
        database: cfg.db.name,
        entities: [Driver, RegistrationStep, Car, DriverDocument, OutboxEvent, DriverRoute, DriverCapabilities],
        migrations: [Init1731360000000, RoutesCapabilities1794756143798],
        migrationsRun: true, // sxema o'zgarishlari src/migrations/ orqali
        synchronize: false,
        retryAttempts: 30,
        retryDelay: 2000,
      }),
    }),
    TypeOrmModule.forFeature([Driver]),
  ],
  providers: [{ provide: DriverStore, useClass: TypeOrmDriverStore }],
  exports: [DriverStore],
})
export class PersistenceModule {}

export function buildAppModule(cfg: AppConfig = loadConfig(), persistence: DynamicModule | typeof PersistenceModule = PersistenceModule) {
  @Module({
    imports: [CoreModule.register(cfg), persistence as never, EventsModule, DriversModule],
    controllers: [HealthController],
  })
  class AppModule {}
  return AppModule;
}
