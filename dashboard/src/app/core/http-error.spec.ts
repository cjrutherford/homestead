import { describe, expect, it } from 'vitest';
import { HttpErrorResponse, HttpHeaders } from '@angular/common/http';
import { isNotSetUp, httpErrorMessage, isUnreachable } from './http-error';

function failure(status: number, error: unknown = null, headers?: HttpHeaders): HttpErrorResponse {
  return new HttpErrorResponse({ status, error, headers });
}

describe('httpErrorMessage', () => {
  it('treats status 0 and gateway errors as unreachable', () => {
    for (const status of [0, 502, 503, 504]) {
      expect(isUnreachable(failure(status))).toBe(true);
      expect(httpErrorMessage(failure(status), 'Login')).toContain("Can't reach the appliance");
    }
    expect(isUnreachable(failure(500))).toBe(false);
  });

  it('maps other 5xx to a recorded appliance failure', () => {
    expect(httpErrorMessage(failure(500), 'Login')).toBe(
      'Login failed on the appliance. The error has been recorded.',
    );
  });

  it('shows the server message on 400', () => {
    expect(httpErrorMessage(failure(400, 'bad json\n'), 'Login')).toBe(
      'The appliance rejected the request: bad json',
    );
    expect(httpErrorMessage(failure(400), 'Login')).toBe('The appliance rejected the request.');
  });

  it('maps 429 with and without Retry-After', () => {
    expect(httpErrorMessage(failure(429), 'Login')).toContain('Wait a while');
    expect(
      httpErrorMessage(failure(429, null, new HttpHeaders({ 'Retry-After': '30' })), 'Login'),
    ).toContain('30 seconds');
  });

  it('maps 401 and 403', () => {
    expect(httpErrorMessage(failure(401), 'Login')).toContain('Log in');
    expect(httpErrorMessage(failure(403), 'Login')).toContain('refused');
  });

  it('prefers per-status overrides', () => {
    expect(httpErrorMessage(failure(401), 'Login', { 401: 'custom' })).toBe('custom');
  });

  it('falls back to the status for other codes', () => {
    expect(httpErrorMessage(failure(418), 'Setup')).toBe('Setup failed (HTTP 418).');
  });

  it('treats a 404 read as not set up yet, and nothing else', () => {
    expect(isNotSetUp(failure(404))).toBe(true);
    expect(isNotSetUp(failure(500))).toBe(false);
    expect(isNotSetUp(failure(0))).toBe(false);
  });
});
