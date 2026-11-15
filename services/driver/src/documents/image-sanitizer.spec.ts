import { detectImageType, InvalidImageError, stripMetadata } from './image-sanitizer';
import { EXIF_MARK, exifPngChunk, makeJpeg, makePng, pngChunk } from '../../test/support/helpers';

describe('image-sanitizer (F-17)', () => {
  it('JPEG: EXIF/COM va EOI dan keyingi ma\'lumot o\'chadi, piksel ma\'lumoti qoladi', () => {
    const src = makeJpeg(true);
    expect(src.includes(EXIF_MARK)).toBe(true);
    const out = stripMetadata(src, 'image/jpeg');
    expect(out.includes(EXIF_MARK)).toBe(false);
    expect(out.includes('comment-secret')).toBe(false);
    expect(out.includes('JFIF')).toBe(true);
    // entropiya (FF00 va RST) buzilmagan
    expect(out.includes(Buffer.from([0x12, 0xff, 0x00, 0x34, 0xff, 0xd0, 0x56]))).toBe(true);
    expect(out.subarray(-2)).toEqual(Buffer.from([0xff, 0xd9]));
    expect(detectImageType(out)).toBe('image/jpeg');
  });

  it('JPEG: metadatasiz fayl o\'zgarishsiz qoladi', () => {
    const src = makeJpeg(false);
    const out = stripMetadata(src, 'image/jpeg');
    expect(out.length).toBe(src.length - Buffer.from('TRAILER-' + EXIF_MARK).length);
  });

  it('PNG: eXIf/tEXt/tIME o\'chadi, kritik chunklar qoladi', () => {
    const src = makePng([exifPngChunk(), pngChunk('tEXt', Buffer.from('Author\0secret')), pngChunk('tIME', Buffer.alloc(7))]);
    const out = stripMetadata(src, 'image/png');
    expect(out.includes(EXIF_MARK)).toBe(false);
    expect(out.includes('secret')).toBe(false);
    expect(out.includes('IDAT')).toBe(true);
    expect(out.includes('IHDR')).toBe(true);
    expect(out.includes('IEND')).toBe(true);
  });

  it('buzilgan fayllar InvalidImageError beradi', () => {
    expect(() => stripMetadata(Buffer.from([0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff]), 'image/jpeg')).toThrow(InvalidImageError);
    expect(() => stripMetadata(makePng().subarray(0, 40), 'image/png')).toThrow(InvalidImageError);
  });

  it('turni magic bytes bo\'yicha aniqlaydi', () => {
    expect(detectImageType(Buffer.from('%PDF-1.4'))).toBeNull();
    expect(detectImageType(Buffer.from('GIF89a......'))).toBeNull();
  });
});
