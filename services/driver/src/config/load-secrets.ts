// Side-effect modul: main.ts dagi BIRINCHI import — boshqa modullar (config) muhitni o'qishidan oldin ishlaydi.
import { loadSecretEnv } from './secret-env';

const names = loadSecretEnv();
if (names.length > 0) {
  // Logger hali yo'q; faqat nomlar (qiymatlar emas)
  process.stdout.write(`${JSON.stringify({ level: 'info', msg: "sirlar fayldan yuklandi", names })}\n`);
}
