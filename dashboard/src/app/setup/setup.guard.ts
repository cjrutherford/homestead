import { inject } from '@angular/core';
import { CanActivateFn, Router, Route } from '@angular/router';
import { catchError, map, of } from 'rxjs';
import { SessionService } from '../auth/session.service';
import { SetupService } from './setup.service';

/**
 * Routes that stay reachable while the walkthrough holds the owner (S287,
 * ADR-034): the pages the walkthrough itself sends the owner to, the way
 * out (login) and the claim page. Everything else redirects to /setup.
 * The operations list and operation detail are reachable so an install the
 * walkthrough started can be followed.
 */
export const SETUP_EXEMPT_PATHS: readonly string[] = [
  'setup',
  'login',
  'claim',
  'account',
  'sign-in',
  'tunnel',
  'exposure',
  'tls',
  'catalog/:name/install',
  'operations',
  'operations/:id',
];

/**
 * The walkthrough gate. It exists only here: the owner is redirected to
 * /setup while the walkthrough is incomplete on a fresh appliance. Not
 * logged in, legacy, complete, or a status that cannot be read all let the
 * navigation through, so the gate can never lock the owner out.
 */
export const setupGuard: CanActivateFn = () => {
  const sessions = inject(SessionService);
  const setup = inject(SetupService);
  const router = inject(Router);
  if (!sessions.isLoggedIn()) {
    return true;
  }
  return setup.setupStatus().pipe(
    map((status) => (status.gated ? router.parseUrl('/setup') : true)),
    catchError(() => of(true)),
  );
};

/** Adds the guard to every route that is not exempt or a redirect/wildcard. */
export function gateRoutes(routes: Route[]): Route[] {
  return routes.map((route) => {
    const path = route.path ?? '';
    if (route.redirectTo !== undefined || path === '**' || SETUP_EXEMPT_PATHS.includes(path)) {
      return route;
    }
    return { ...route, canActivate: [...(route.canActivate ?? []), setupGuard] };
  });
}
