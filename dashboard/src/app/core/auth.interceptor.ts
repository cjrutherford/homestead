import { HttpErrorResponse, HttpInterceptorFn } from '@angular/common/http';
import { inject } from '@angular/core';
import { Router } from '@angular/router';
import { catchError, throwError } from 'rxjs';
import { SessionService } from '../auth/session.service';

/** Name of the script-readable CSRF cookie set at login (ADR-020). */
export const CSRF_COOKIE = 'homestead_csrf';
export const CSRF_HEADER = 'X-CSRF-Token';

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

/**
 * Session transport for the dashboard (ADR-020, S256): same-origin requests
 * carry the HttpOnly session cookie by themselves; every mutating API call
 * also echoes the CSRF cookie in X-CSRF-Token. A 401 from the API (other
 * than a failed login) means the session ended, so the local hint is
 * dropped and the user is sent to the login page.
 */
export const authInterceptor: HttpInterceptorFn = (req, next) => {
  const sessions = inject(SessionService);
  const router = inject(Router);
  const isApi = req.url.startsWith('/api/');
  if (isApi && MUTATING.has(req.method)) {
    const csrf = readCookie(CSRF_COOKIE);
    if (csrf) {
      req = req.clone({ setHeaders: { [CSRF_HEADER]: csrf } });
    }
  }
  const isLogin = req.method === 'POST' && req.url === '/api/v1/sessions';
  return next(req).pipe(
    catchError((err: unknown) => {
      if (
        isApi &&
        !isLogin &&
        err instanceof HttpErrorResponse &&
        err.status === 401
      ) {
        sessions.expire();
        const path = router.url.split('?')[0];
        if (path !== '/login' && path !== '/claim') {
          void router.navigateByUrl('/login');
        }
      }
      return throwError(() => err);
    }),
  );
};
