// F-16: Docker secrets — birinchi import (config muhitni o'qishidan oldin)
import './config/load-secrets';
import 'reflect-metadata';
import { Logger } from '@nestjs/common';
import { NestFactory } from '@nestjs/core';
import { configureApp } from './app.setup';
import { buildAppModule } from './app.module';
import { loadConfig } from './config/configuration';
import { PiiMaskingLogger } from './common/pii';

async function bootstrap(): Promise<void> {
  const cfg = loadConfig();
  // Loglarda telefon/email maskalanadi (998**___**67, s***@***.com)
  const logger = new PiiMaskingLogger();
  logger.setLogLevels(['log', 'error', 'warn']);
  const app = await NestFactory.create(buildAppModule(cfg), { logger });
  configureApp(app);
  app.enableShutdownHooks();
  await app.listen(cfg.port, '0.0.0.0');
  new Logger('Bootstrap').log(`Driver running on :${cfg.port}`);
}

void bootstrap();
