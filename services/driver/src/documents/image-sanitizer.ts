/**
 * Metadata (EXIF/GPS/XMP/IPTC, matn bloklari) ni olib tashlaydi (F-17).
 * Toza JS — native modul kerak emas. Piksellar qayta kodlanmaydi, shuning uchun sifat o'zgarmaydi.
 *
 * JPEG: APP1 (EXIF/XMP), APP3–APP13, APP15 va COM o'chiriladi; rang uchun kerakli APP0 (JFIF),
 *       APP2 (ICC) va APP14 (Adobe) qoladi. EOI dan keyingi qo'shimcha ma'lumot (masalan MPF/preview) tashlanadi.
 * PNG:  faqat ruxsat etilgan chunklar qoladi; eXIf, tEXt, zTXt, iTXt, tIME va noma'lum chunklar o'chiriladi.
 * Eslatma: EXIF Orientation ham o'chadi — klient rasmni yuborishdan oldin burishi kerak.
 */

export class InvalidImageError extends Error {}

export type ImageType = 'image/jpeg' | 'image/png';

const PNG_SIG = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);

/** Fayl boshidagi "magic bytes" bo'yicha turni aniqlaydi. */
export function detectImageType(buf: Buffer): ImageType | null {
  if (buf.length >= 3 && buf[0] === 0xff && buf[1] === 0xd8 && buf[2] === 0xff) return 'image/jpeg';
  if (buf.length >= 8 && buf.subarray(0, 8).equals(PNG_SIG)) return 'image/png';
  return null;
}

export function stripMetadata(buf: Buffer, type: ImageType): Buffer {
  return type === 'image/jpeg' ? stripJpeg(buf) : stripPng(buf);
}

const KEEP_JPEG_APP = new Set([0xe0, 0xe2, 0xee]); // APP0, APP2, APP14

function stripJpeg(buf: Buffer): Buffer {
  if (buf.length < 4 || buf[0] !== 0xff || buf[1] !== 0xd8) throw new InvalidImageError('JPEG SOI yo\'q');
  const out: Buffer[] = [buf.subarray(0, 2)];
  let i = 2;
  let sawScan = false;

  while (i < buf.length) {
    if (buf[i] !== 0xff) throw new InvalidImageError('JPEG marker kutilgan');
    // to'ldiruvchi 0xFF baytlarini o'tkazamiz
    while (i < buf.length && buf[i] === 0xff) i++;
    if (i >= buf.length) throw new InvalidImageError('JPEG tugab qoldi');
    const marker = buf[i++];

    if (marker === 0xd9) { // EOI
      out.push(Buffer.from([0xff, 0xd9]));
      if (!sawScan) throw new InvalidImageError('JPEG da piksel ma\'lumoti yo\'q');
      return Buffer.concat(out);
    }
    if (marker === 0x01 || (marker >= 0xd0 && marker <= 0xd8)) { // parametrsiz markerlar
      out.push(Buffer.from([0xff, marker]));
      continue;
    }
    if (i + 2 > buf.length) throw new InvalidImageError('JPEG segment uzunligi yo\'q');
    const len = buf.readUInt16BE(i);
    if (len < 2 || i + len > buf.length) throw new InvalidImageError('JPEG segment uzunligi noto\'g\'ri');
    const segEnd = i + len;
    const isMeta = (marker >= 0xe0 && marker <= 0xef && !KEEP_JPEG_APP.has(marker)) || marker === 0xfe;

    if (!isMeta) {
      out.push(Buffer.from([0xff, marker]), buf.subarray(i, segEnd));
    }
    i = segEnd;

    if (marker === 0xda) { // SOS: undan keyin entropiya bilan kodlangan ma'lumot — keyingi haqiqiy markergacha
      sawScan = true;
      const start = i;
      while (i < buf.length) {
        if (buf[i] === 0xff) {
          const n = buf[i + 1];
          if (n === undefined) throw new InvalidImageError('JPEG tugab qoldi');
          if (n === 0x00 || (n >= 0xd0 && n <= 0xd7) || n === 0xff) { i += n === 0xff ? 1 : 2; continue; }
          break; // haqiqiy marker
        }
        i++;
      }
      out.push(buf.subarray(start, i));
    }
  }
  throw new InvalidImageError('JPEG EOI siz tugagan');
}

const KEEP_PNG_CHUNKS = new Set([
  'IHDR', 'PLTE', 'IDAT', 'IEND', 'tRNS', 'gAMA', 'cHRM', 'sRGB', 'iCCP', 'sBIT', 'bKGD', 'pHYs',
]);

function stripPng(buf: Buffer): Buffer {
  if (!buf.subarray(0, 8).equals(PNG_SIG)) throw new InvalidImageError('PNG imzosi noto\'g\'ri');
  const out: Buffer[] = [PNG_SIG];
  let i = 8;
  let first = true;
  let sawIdat = false;

  while (i < buf.length) {
    if (i + 12 > buf.length) throw new InvalidImageError('PNG chunk tugab qoldi');
    const len = buf.readUInt32BE(i);
    const type = buf.toString('latin1', i + 4, i + 8);
    const total = 12 + len;
    if (len > 0x7fffffff || i + total > buf.length) throw new InvalidImageError('PNG chunk uzunligi noto\'g\'ri');
    if (first && type !== 'IHDR') throw new InvalidImageError('PNG IHDR birinchi bo\'lishi kerak');
    first = false;

    if (KEEP_PNG_CHUNKS.has(type)) out.push(buf.subarray(i, i + total));
    if (type === 'IDAT') sawIdat = true;
    i += total;
    if (type === 'IEND') {
      if (!sawIdat) throw new InvalidImageError('PNG da piksel ma\'lumoti yo\'q');
      return Buffer.concat(out); // IEND dan keyingi ortiqcha baytlar tashlanadi
    }
  }
  throw new InvalidImageError('PNG IEND siz tugagan');
}
