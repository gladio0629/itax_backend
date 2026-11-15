import { Inject, Injectable } from '@nestjs/common';
import { mkdir, readFile, rename, rm, writeFile } from 'fs/promises';
import { dirname, join, resolve, sep } from 'path';
import { APP_CONFIG, AppConfig } from '../config/configuration';

/** Fayl saqlash abstraksiyasi. Hozir lokal papka (uploads/), keyinroq S3 implementatsiyasi bilan almashtiriladi. */
export abstract class FileStorage {
  abstract put(key: string, data: Buffer): Promise<void>;
  abstract get(key: string): Promise<Buffer>;
  abstract delete(key: string): Promise<void>;
}

@Injectable()
export class LocalFileStorage extends FileStorage {
  private readonly root: string;

  constructor(@Inject(APP_CONFIG) cfg: AppConfig) {
    super();
    this.root = resolve(cfg.uploads.dir);
  }

  private path(key: string): string {
    const p = resolve(join(this.root, key));
    if (!p.startsWith(this.root + sep)) throw new Error('storage: yo\'l papkadan tashqarida');
    return p;
  }

  async put(key: string, data: Buffer): Promise<void> {
    const p = this.path(key);
    await mkdir(dirname(p), { recursive: true });
    const tmp = `${p}.tmp`;
    await writeFile(tmp, data, { mode: 0o640 });
    await rename(tmp, p); // atomik: yarim yozilgan fayl ko'rinmaydi
  }

  get(key: string): Promise<Buffer> {
    return readFile(this.path(key));
  }

  async delete(key: string): Promise<void> {
    await rm(this.path(key), { force: true });
  }
}
