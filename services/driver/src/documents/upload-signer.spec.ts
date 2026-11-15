import { UploadSigner } from './upload-signer';

describe('UploadSigner', () => {
  const claims = { documentId: 'd', driverId: 'u', contentType: 'image/png', sizeBytes: 10 };

  it('to\'g\'ri imzo ok, o\'zgartirilgan maydon invalid, muddati o\'tgan expired', () => {
    let now = 1_000_000;
    const s = new UploadSigner('secret-secret-secret', () => now);
    const { expires, signature } = s.sign(claims, 60);
    expect(s.check({ ...claims, expires }, signature)).toBe('ok');
    expect(s.check({ ...claims, driverId: 'other', expires }, signature)).toBe('invalid');
    expect(s.check({ ...claims, sizeBytes: 11, expires }, signature)).toBe('invalid');
    expect(s.check({ ...claims, expires: expires + 1000 }, signature)).toBe('invalid');
    expect(s.check({ ...claims, expires }, 'zz')).toBe('invalid');
    now += 61_000;
    expect(s.check({ ...claims, expires }, signature)).toBe('expired');
  });
});
