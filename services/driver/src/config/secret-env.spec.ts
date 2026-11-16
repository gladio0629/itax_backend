import { mkdirSync, mkdtempSync, symlinkSync, writeFileSync } from 'fs';
import { tmpdir } from 'os';
import { join } from 'path';
import { loadSecretEnv } from './secret-env';

describe('loadSecretEnv (Docker secrets)', () => {
  const mk = () => mkdtempSync(join(tmpdir(), 'izgo-sec-'));

  it('fayllarni yuklaydi; aniq env ustun; noto\'g\'ri nom, papka o\'tkaziladi; symlink kuzatiladi', () => {
    const dir = mk();
    writeFileSync(join(dir, 'DB_PASSWORD'), 's3cret\n');
    writeFileSync(join(dir, 'INTERNAL_TOKENS'), 'a,b\r\n');
    writeFileSync(join(dir, 'KEEP'), 'from-file');
    writeFileSync(join(dir, 'EMPTYENV'), 'file-wins');
    writeFileSync(join(dir, 'lowercase'), 'x');
    mkdirSync(join(dir, 'SUBDIR'));
    symlinkSync(join(dir, 'KEEP'), join(dir, 'LINK'));
    const env: NodeJS.ProcessEnv = { KEEP: 'from-env', EMPTYENV: '' };
    expect(loadSecretEnv(dir, env)).toEqual(['DB_PASSWORD', 'EMPTYENV', 'INTERNAL_TOKENS', 'LINK']);
    expect(env).toEqual({ DB_PASSWORD: 's3cret', INTERNAL_TOKENS: 'a,b', KEEP: 'from-env', EMPTYENV: 'file-wins', LINK: 'from-file' });
  });

  it('papka yo\'q — hech narsa; katta fayl — xato', () => {
    const env: NodeJS.ProcessEnv = {};
    expect(loadSecretEnv(join(mk(), 'yoq'), env)).toEqual([]);
    expect(env).toEqual({});
    const dir = mk();
    writeFileSync(join(dir, 'BIG'), 'x'.repeat(64 * 1024 + 1));
    expect(() => loadSecretEnv(dir, {})).toThrow(/BIG/);
  });
});
