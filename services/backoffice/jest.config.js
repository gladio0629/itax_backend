// Barcha testlar: src/**/*.spec.ts va test/**/*.spec.ts (Postgres/NATS shart emas — pg-mem va fake lar)
module.exports = {
  preset: 'ts-jest',
  testEnvironment: 'node',
  rootDir: '.',
  roots: ['<rootDir>/src', '<rootDir>/test'],
  testRegex: '.*\\.spec\\.ts$',
  // Har test/hook uchun: createHarness (Nest + pg-mem + migratsiyalar) yuklangan mashinada 5 s dan oshishi mumkin
  testTimeout: 30_000,
  transform: { '^.+\\.ts$': ['ts-jest', { tsconfig: 'tsconfig.spec.json' }] },
};
