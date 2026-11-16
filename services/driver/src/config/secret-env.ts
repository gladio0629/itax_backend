import { existsSync, readdirSync, readFileSync, statSync } from 'fs';
import { join } from 'path';

/**
 * Docker secrets (F-16): SECRETS_DIR (default /run/secrets/izgo) papkasidagi har bir fayl shu nomli muhit
 * o'zgaruvchisiga yuklanadi (fayl nomi = o'zgaruvchi nomi: DB_PASSWORD, INTERNAL_TOKENS, ...).
 * Qoidalar shared/go/secretenv va Spring configtree bilan bir xil:
 *  - muhitda bo'sh bo'lmagan qiymat bo'lsa — u ustun (testlar, lokal ishga tushirish);
 *  - papka yo'q — hech narsa qilinmaydi;
 *  - faqat ^[A-Z][A-Z0-9_]*$ nomli oddiy fayllar (symlink kuzatiladi — Kubernetes secret volume);
 *  - oxiridagi bitta \n yoki \r\n olib tashlanadi; 64 KiB dan katta fayl — xato.
 * Qiymatlar hech qayerga yozilmaydi; yuklangan NOMLAR qaytariladi.
 */
export const DEFAULT_SECRETS_DIR = '/run/secrets/izgo';
const NAME_RE = /^[A-Z][A-Z0-9_]*$/;
const MAX_SIZE = 64 * 1024;

export function loadSecretEnv(dir: string = process.env.SECRETS_DIR || DEFAULT_SECRETS_DIR, env: NodeJS.ProcessEnv = process.env): string[] {
  if (!existsSync(dir)) return [];
  const loaded: string[] = [];
  for (const name of readdirSync(dir)) {
    if (!NAME_RE.test(name)) continue;
    if (env[name]) continue;
    const path = join(dir, name);
    const st = statSync(path); // symlink kuzatiladi
    if (!st.isFile()) continue;
    if (st.size > MAX_SIZE) throw new Error(`secret-env: ${name}: ${st.size} bayt (chegara ${MAX_SIZE})`);
    env[name] = readFileSync(path, 'utf8').replace(/\r?\n$/, '');
    loaded.push(name);
  }
  return loaded.sort();
}
