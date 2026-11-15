import { JwtVerifier, JwtVerifyError } from './jwt.verifier';
import { forgedToken, ISSUER, makeToken, PUBLIC_PEM } from '../../test/support/helpers';

describe('JwtVerifier', () => {
  const v = new JwtVerifier(PUBLIC_PEM, ISSUER, 'driver');
  const reason = (t: string) => { try { v.verifyAccess(t); return 'ok'; } catch (e) { return (e as JwtVerifyError).reason; } };

  it('to\'g\'ri access token', () => expect(v.verifyAccess(makeToken()).sub).toBe('11111111-1111-4111-8111-111111111111'));
  it('begona kalit bilan imzolangan', () => expect(reason(forgedToken())).toBe('invalid'));
  it('muddati tugagan', () => expect(reason(makeToken({ exp: Math.floor(Date.now() / 1000) - 5 }))).toBe('expired'));
  it('refresh token (typ)', () => expect(reason(makeToken({ typ: 'refresh' }))).toBe('invalid'));
  it('boshqa issuer', () => expect(reason(makeToken({ iss: 'evil' }))).toBe('invalid'));
  it('boshqa audience (client)', () => expect(reason(makeToken({ aud: ['client'] }))).toBe('wrong_audience'));
  it('bir nechta audience', () => expect(reason(makeToken({ aud: ['driver', 'admin'] }))).toBe('invalid'));
  it('exp yo\'q', () => expect(reason(makeToken({ exp: undefined }))).toBe('invalid'));
  it('alg=none / HS256', () => {
    expect(reason(makeToken({}, { header: { alg: 'none' } }))).toBe('invalid');
    expect(reason(makeToken({}, { header: { alg: 'HS256' } }))).toBe('invalid');
  });
  it('sub UUID emas', () => expect(reason(makeToken({ sub: 'x' }))).toBe('invalid'));
  it('axlat', () => { expect(reason('abc')).toBe('invalid'); expect(reason('a.b.c')).toBe('invalid'); });
});
