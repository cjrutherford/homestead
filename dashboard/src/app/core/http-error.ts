import { HttpErrorResponse } from '@angular/common/http';

/** Per-status overrides for {@link httpErrorMessage}. */
export type HttpErrorOverrides = Partial<Record<number, string>>;

/** True when the appliance could not be reached (no response or a gateway error). */
export function isUnreachable(err: HttpErrorResponse): boolean {
  return err.status === 0 || err.status === 502 || err.status === 503 || err.status === 504;
}

/**
 * True when a read answered 404 because the thing it describes has
 * not been set up yet (no saved TLS strategy, no reviewed exposure plan).
 * Pages treat this as a state with a next action, not as an error. Angular's
 * HttpClient writes nothing to the console for a handled error; the
 * browser's own network log still lists the request (see DEFERRED.md).
 */
export function isNotSetUp(err: HttpErrorResponse): boolean {
  return err.status === 404;
}

/** The server's plain-text error body, when it sent one. */
function serverMessage(err: HttpErrorResponse): string {
  const body: unknown = err.error;
  return typeof body === 'string' ? body.trim() : '';
}

/** Seconds from a Retry-After header, or null when absent or not a number. */
function retryAfterSeconds(err: HttpErrorResponse): number | null {
  const seconds = Number(err.headers?.get('Retry-After'));
  return Number.isFinite(seconds) && seconds > 0 ? Math.ceil(seconds) : null;
}

/**
 * Maps an HTTP failure to a plain message so a refusal, a lockout, and an
 * unreachable appliance never read alike. `subject` names the action that
 * failed (for example "Login"); `overrides` replaces the text for a status
 * where the page knows better.
 */
export function httpErrorMessage(
  err: HttpErrorResponse,
  subject: string,
  overrides: HttpErrorOverrides = {},
): string {
  const override = overrides[err.status];
  if (override !== undefined) {
    return override;
  }
  if (isUnreachable(err)) {
    return "Can't reach the appliance. Check that homestead-server is running.";
  }
  switch (err.status) {
    case 400:
      return serverMessage(err)
        ? `The appliance rejected the request: ${serverMessage(err)}`
        : 'The appliance rejected the request.';
    case 401:
      return 'Your session is not valid. Log in and try again.';
    case 403:
      return 'The appliance refused the request.';
    case 429: {
      const seconds = retryAfterSeconds(err);
      return seconds === null
        ? 'Too many attempts. Wait a while and try again.'
        : `Too many attempts. Try again in ${seconds} seconds.`;
    }
  }
  if (err.status >= 500) {
    return `${subject} failed on the appliance. The error has been recorded.`;
  }
  return `${subject} failed (HTTP ${err.status}).`;
}
