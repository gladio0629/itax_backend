import { Global, Module } from '@nestjs/common';
import { APP_CONFIG, AppConfig } from '../config/configuration';
import { Errors } from '../common/errors';
import { HttpOrdersPort } from './http-orders';
import { HttpDriverLookupPort, HttpIdentityStaffPort, HttpUserLookupPort } from './http-ports';
import { InternalHttpClient } from './internal-http';
import {
  MockData, MockDriverLookupPort, MockDriversPort, MockIdentityStaffPort, MockOrdersPort, MockPassengersPort,
  MockStatsPort, MockUserLookupPort, MockWalletPort,
} from './mock-platform';
import {
  DriverLookupPort, DriversPort, IdentityStaffPort, OrdersPort, PassengersPort, StatsPort, UserLookupPort, WalletPort,
} from './ports';

/**
 * Boshqa servislar bilan aloqa.
 *  - IdentityStaffPort — haqiqiy HTTP (IDENTITY_STAFF_MODE=http, default); testlarda mock.
 *  - Driver/User lookup — HTTP (INTERNAL_LOOKUP_MODE=http; docker-compose'da default). Kod defaulti va testlarda MOCK.
 *  - OrdersPort — Trip & Order ichki API (ORDERS_MODE=http; docker-compose'da). Kod defaulti va testlarda MOCK.
 *  - Qolganlari (drivers/passengers CRUD, wallet, stats dagi haydovchi/yo'lovchi raqamlari) hozir mock.
 */
@Global()
@Module({
  providers: [
    MockData,
    { provide: DriversPort, useClass: MockDriversPort },
    { provide: PassengersPort, useClass: MockPassengersPort },
    {
      provide: OrdersPort,
      inject: [APP_CONFIG, MockData],
      useFactory: (cfg: AppConfig, d: MockData) => cfg.orders.mode === 'http'
        ? new HttpOrdersPort(new InternalHttpClient(
          cfg.orders.baseUrl, cfg.identity.internalToken, cfg.identity.timeoutMs, fetch, Errors.tripOrderUnavailable,
        ))
        : new MockOrdersPort(d, cfg),
    },
    { provide: WalletPort, useClass: MockWalletPort },
    { provide: StatsPort, useClass: MockStatsPort },
    {
      provide: IdentityStaffPort,
      inject: [APP_CONFIG, MockData],
      useFactory: (cfg: AppConfig, d: MockData) => cfg.identity.mode === 'http'
        ? new HttpIdentityStaffPort(new InternalHttpClient(cfg.identity.baseUrl, cfg.identity.internalToken, cfg.identity.timeoutMs))
        : new MockIdentityStaffPort(d),
    },
    {
      provide: DriverLookupPort,
      inject: [APP_CONFIG, MockData],
      useFactory: (cfg: AppConfig, d: MockData) => cfg.lookups.mode === 'http'
        ? new HttpDriverLookupPort(new InternalHttpClient(cfg.lookups.driverBaseUrl, cfg.identity.internalToken, cfg.identity.timeoutMs))
        : new MockDriverLookupPort(d),
    },
    {
      provide: UserLookupPort,
      inject: [APP_CONFIG, MockData],
      useFactory: (cfg: AppConfig, d: MockData) => cfg.lookups.mode === 'http'
        ? new HttpUserLookupPort(new InternalHttpClient(cfg.identity.baseUrl, cfg.identity.internalToken, cfg.identity.timeoutMs))
        : new MockUserLookupPort(d),
    },
  ],
  exports: [MockData, DriversPort, PassengersPort, OrdersPort, WalletPort, StatsPort, IdentityStaffPort, DriverLookupPort, UserLookupPort],
})
export class PortsModule {}
