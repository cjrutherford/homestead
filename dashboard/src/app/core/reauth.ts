import { HttpErrorResponse, HttpEvent, HttpInterceptorFn } from '@angular/common/http';
import { Injectable, Type, inject, signal } from '@angular/core';
import { Observable, catchError, switchMap, throwError } from 'rxjs';

/** Carries the local password for fresh re-authentication (ADR-033). */
export const REAUTH_HEADER = 'X-Homestead-Reauth';
export const REAUTH_REQUIRED = 'reauth-required';
export const REAUTH_INVALID = 'reauth-invalid';
/** A People action on the public address needs a fresh Authentik sign-in (ADR-049). */
export const STEPUP_REQUIRED = 'stepup-required';
/** A step-up route on the public address that only works locally (S335). */
export const LOCAL_ONLY = 'local-only';

/** A pending request for the local password. */
export interface ReauthRequest {
  /** True when the previous password was wrong. */
  invalid: boolean;
  /**
   * True on the public address: the dialog offers a fresh Authentik sign-in
   * instead of the password (ADR-049). The page navigates away, so the
   * request is not retried.
   */
  stepUp?: boolean;
  /**
   * True when the action only works from the local or overlay address: the
   * dialog explains that, with no password field and no Authentik button.
   */
  localOnly?: boolean;
  /** Answers with the password, or null when the owner cancelled. */
  answer: (password: string | null) => void;
}

/**
 * Fresh re-authentication (ADR-033, S286): link, unlink and the step-up
 * routes ask for the local password again. The interceptor below holds the
 * request, shows the dialog once and retries with the password; the page
 * that made the request needs no code of its own. The password lives in the
 * dialog and one request header, nowhere else.
 */
@Injectable({ providedIn: 'root' })
export class Reauth {
  readonly request = signal<ReauthRequest | null>(null);
  /**
   * The dialog component, loaded the first time a password is asked for so
   * the initial bundle does not carry it. Set while a request is pending.
   */
  readonly dialog = signal<Type<unknown> | null>(null);

  ask(invalid: boolean, stepUp = false, localOnly = false): Observable<string | null> {
    return new Observable((subscriber) => {
      void import('../ui/reauth-dialog').then((m) => this.dialog.set(m.ReauthDialog));
      this.request.set({
        invalid,
        stepUp,
        localOnly,
        answer: (password) => {
          this.request.set(null);
          this.dialog.set(null);
          subscriber.next(password);
          subscriber.complete();
        },
      });
    });
  }
}

export const reauthInterceptor: HttpInterceptorFn = (req, next) => {
  const reauth = inject(Reauth);
  const attempt = (request: typeof req): Observable<HttpEvent<unknown>> =>
    next(request).pipe(
      catchError((err: unknown) => {
        const code =
          err instanceof HttpErrorResponse && err.status === 403 ? err.headers?.get('X-Homestead-Error') : null;
        if (code === STEPUP_REQUIRED) {
          return reauth.ask(false, true).pipe(switchMap(() => throwError(() => err)));
        }
        if (code === LOCAL_ONLY) {
          return reauth.ask(false, false, true).pipe(switchMap(() => throwError(() => err)));
        }
        if (code !== REAUTH_REQUIRED && code !== REAUTH_INVALID) {
          return throwError(() => err);
        }
        return reauth.ask(code === REAUTH_INVALID).pipe(
          switchMap((password) =>
            password === null
              ? throwError(() => err)
              : attempt(req.clone({ setHeaders: { [REAUTH_HEADER]: password } })),
          ),
        );
      }),
    );
  return attempt(req);
};
