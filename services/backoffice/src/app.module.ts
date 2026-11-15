import { DynamicModule, Global, Module, Type } from '@nestjs/common';
import { APP_GUARD } from '@nestjs/core';
import { TypeOrmModule } from '@nestjs/typeorm';
import { AuditController } from './audit/audit.controller';
import { AuditService } from './audit/audit.service';
import { AdminGuard, JWT_VERIFIER } from './auth/admin.guard';
import { JwtVerifier } from './auth/jwt.verifier';
import { RedisSessionRevocationPort, SessionRevocationPort } from './auth/session-revocation';
import { APP_CONFIG, AppConfig, loadConfig } from './config/configuration';
import { ENTITIES } from './entities';
import { ComplaintsModule } from './complaints/complaints.module';
import { EventsModule } from './events/events.module';
import { HealthController } from './health.controller';
import { Init1731450000000 } from './migrations/1731450000000-init';
import { AuditImmutable1731450000001 } from './migrations/1731450000001-audit-immutable';
import { Complaints1731450000002 } from './migrations/1731450000002-complaints';
import { OperatorModule } from './operator/operator.module';
import { OrdersController } from './orders/orders.controller';
import { PersonsModule } from './persons/persons.module';
import { PortsModule } from './ports/ports.module';
import { ROLES, RolesCatalog } from './roles/roles.catalog';
import { StaffController } from './staff/staff.controller';
import { StaffService } from './staff/staff.service';
import { StatsController } from './stats/stats.controller';
import { TariffsController } from './tariffs/tariffs.controller';
import { TariffsService } from './tariffs/tariffs.service';
import { WalletController } from './wallet/wallet.controller';
import { WalletService } from './wallet/wallet.service';

@Global()
@Module({})
export class CoreModule {
  static register(cfg: AppConfig): DynamicModule {
    const providers = [
      { provide: APP_CONFIG, useValue: cfg },
      { provide: ROLES, useValue: new RolesCatalog(cfg.rolesFile) },
      { provide: JWT_VERIFIER, useValue: new JwtVerifier(cfg.jwt.publicKeyPem, cfg.jwt.issuer, 'admin') },
      // Logout qilingan sessiyalar (Redis). Testlarda harness xotiradagi implementatsiya bilan almashtiradi.
      {
        provide: SessionRevocationPort,
        useFactory: () => new RedisSessionRevocationPort(cfg.redis.url, cfg.redis.timeoutMs),
      },
      AuditService,
      // Barcha endpointlar default-deny: har biri @Perm/@Authenticated/@Public deklaratsiyasini talab qiladi
      { provide: APP_GUARD, useClass: AdminGuard },
    ];
    return { module: CoreModule, providers, exports: [APP_CONFIG, ROLES, JWT_VERIFIER, AuditService, SessionRevocationPort] };
  }
}

export function postgresModule(cfg: AppConfig): DynamicModule {
  return TypeOrmModule.forRoot({
    type: 'postgres',
    host: cfg.db.host,
    port: cfg.db.port,
    username: cfg.db.user,
    password: cfg.db.password,
    database: cfg.db.name,
    entities: ENTITIES,
    migrations: [Init1731450000000, AuditImmutable1731450000001, Complaints1731450000002],
    migrationsRun: true,
    synchronize: false,
    retryAttempts: 30,
    retryDelay: 2000,
  });
}

/** `db` — testlarda pg-mem moduli beriladi. */
export function buildAppModule(cfg: AppConfig = loadConfig(), db: DynamicModule | Type<unknown> = postgresModule(cfg)) {
  @Module({
    imports: [CoreModule.register(cfg), db as never, PortsModule, OperatorModule, EventsModule, PersonsModule, ComplaintsModule],
    controllers: [
      HealthController, StaffController, OrdersController, StatsController, WalletController, TariffsController, AuditController,
    ],
    providers: [StaffService, WalletService, TariffsService],
  })
  class AppModule {}
  return AppModule;
}
