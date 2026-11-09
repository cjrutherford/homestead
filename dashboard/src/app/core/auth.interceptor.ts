import { HttpErrorResponse, HttpInterceptorFn } from '@angular/common/http';
import { inject } from '@angular/core';
import { Router } from '@angular/router';
import { catchError, throwError } from 'rxjs';
import { SessionService } from '../auth/session.service';

/** Name of the script-readable CSRF cookie set at login (ADR-020). */
export const CSRF_COOKIE = 'homestead_csrf';
/** Over TLS the daemon sets the __Host- name (ADR-061, S470). */
export const HOST_CSRF_COOKIE = '__Host-homestead_csrf';
export const CSRF_HEADER = 'X-CSRF-Token';

/** Marks a 401 that is a wrong current password, not a dead session (S274). */
export const ERROR_CODE_HEADER = 'X-Homestead-Error';
export const WRONG_CURRENT_PASSWORD = 'invalid-current-password';
/** A person session asked for an owner-only route (S338, ADR-050 §2). */
export const OWNER_ONLY = 'owner-only';

export function isOwnerOnly(err: unknown): boolean {
  return (
    err instanceof HttpErrorResponse &&
    err.status === 403 &&
    err.headers?.get(ERROR_CODE_HEADER) === OWNER_ONLY
  );
}

const MUTATING = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

export function readCookie(name: string, source: string = document.cookie): string | null {
  for (const part of source.split(';')) {
    const [key, ...rest] = part.trim().split('=');
    if (key === name) {
      return decodeURIComponent(rest.join('='));
    }
  }
  return null;
}

/** The CSRF token under whichever name the daemon set for this scheme. */
export function readCsrf(source: string = document.cookie): string | null {
  return readCookie(HOST_CSRF_COOKIE, source) ?? readCookie(CSRF_COOKIE, source);
}

/**
 * Session transport for the dashboard (ADR-020, S256): same-origin requests
 * carry the HttpOnly session cookie by themselves; every mutating API call
 * also echoes the CSRF cookie in X-CSRF-Token. A 401 from the API (other
 * than a failed login or a wrong current password on password change) means the session ended, so the local hint is
 * dropped and the user is sent to the login page.
 */
export const authInterceptor: HttpInterceptorFn = (req, next) => {
  const sessions = inject(SessionService);
  const router = inject(Router);
  const isApi = req.url.startsWith('/api/');
  if (isApi && MUTATING.has(req.method)) {
    const csrf = readCsrf();
    if (csrf) {
      req = req.clone({ setHeaders: { [CSRF_HEADER]: csrf } });
    }
  }
  const isLogin = req.method === 'POST' && req.url === '/api/v1/sessions';
  // Only the password-change route's coded 401 is a form error; the
  // same header anywhere else must not keep a dead session alive.
  const isPasswordChange = req.method === 'POST' && req.url === '/api/v1/admins/password';
  return next(req).pipe(
    catchError((err: unknown) => {
      const isWrongCurrentPassword =
        isPasswordChange &&
        err instanceof HttpErrorResponse &&
        err.headers?.get(ERROR_CODE_HEADER) === WRONG_CURRENT_PASSWORD;
      if (
        isApi &&
        !isLogin &&
        err instanceof HttpErrorResponse &&
        err.status === 401 &&
        !isWrongCurrentPassword
      ) {
        sessions.expire();
        const path = router.url.split('?')[0];
        if (path !== '/login' && path !== '/claim') {
          void router.navigateByUrl('/login');
        }
      }
      if (isApi && isOwnerOnly(err)) {
        // Not a session problem: remember the role and do not retry.
        sessions.noteOwnerOnly();
      }
      return throwError(() => err);
    }),
  );
};
