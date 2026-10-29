import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { Observable, map, of, switchMap } from 'rxjs';
import { SessionService, SessionRole } from '../auth/session.service';
import { SetupService } from '../setup/setup.service';
import { setupGate } from '../setup/setup.guard';

/** The session's role: already known, or asked of the server once. */
function roleOf(sessions: SessionService): Observable<SessionRole | null> {
  const known = sessions.role();
  return known !== null ? of(known) : sessions.loadRole();
}

/**
 * The apps page (ADR-050 §4). A signed-out visitor goes to sign-in: the
 * login page, which on the public name hands over to Authentik. A person
 * goes straight in; the owner passes the walkthrough gate like every
 * other page, so a fresh appliance still lands on /setup.
 */
export const homeGuard: CanActivateFn = () => {
  const sessions = inject(SessionService);
  const setup = inject(SetupService);
  const router = inject(Router);
  if (!sessions.isLoggedIn()) {
    return router.parseUrl('/login');
  }
  return roleOf(sessions).pipe(
    switchMap((role) => (role === 'person' ? of(true) : setupGate(setup, router))),
  );
};

/**
 * Everything under /admin is the owner's. A person session is sent to the
 * apps page; the server refuses it anyway (owner-only). A signed-out
 * visitor passes, as before: the pages themselves ask for sign-in.
 */
export const ownerGuard: CanActivateFn = () => {
  const sessions = inject(SessionService);
  const router = inject(Router);
  if (!sessions.isLoggedIn()) {
    return true;
  }
  return roleOf(sessions).pipe(map((role) => (role === 'person' ? router.parseUrl('/') : true)));
};
