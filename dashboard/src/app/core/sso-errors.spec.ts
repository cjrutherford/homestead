import { describe, expect, it } from 'vitest';
import { ssoErrorMessage } from './sso-errors';

describe('ssoErrorMessage', () => {
  it('uses the fixed sentence when Authentik did not answer', () => {
    expect(ssoErrorMessage('unavailable')).toBe('Authentik did not answer. Use your local password.');
  });

  it('says nothing when there is no code', () => {
    expect(ssoErrorMessage(null)).toBeNull();
    expect(ssoErrorMessage('')).toBeNull();
  });

  it('explains an unlinked account, a stale sign-in and an address the plan does not cover', () => {
    expect(ssoErrorMessage('unlinked')).toContain('not linked');
    expect(ssoErrorMessage('state')).toContain('expired');
    expect(ssoErrorMessage('origin')).toContain('this address');
  });

  it('never repeats an unknown code back; it falls back to a plain sentence', () => {
    const message = ssoErrorMessage('<script>alert(1)</script>');
    expect(message).toBe('Sign-in with Authentik did not finish. Use your local password.');
  });
});
