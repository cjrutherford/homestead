import { Observable, of, switchMap } from 'rxjs';
import { catchError, map } from 'rxjs/operators';
import { ApiService, CatalogManifest, EdgeRoute, OperationStarted } from '../core/api.service';

/**
 * The routes in the edge table that belong to one app: the craft app's own
 * route, or a route to one of its served containers on a host named for the
 * app (the installer's default names). Service names alone are not enough:
 * Authentik and Immich both call theirs "server".
 */
export function appRoutes(manifest: CatalogManifest, table: EdgeRoute[]): EdgeRoute[] {
  const served = manifest.Services.filter((service) => service.Exposes !== '').map((service) => service.Name);
  return table.filter(
    (route) =>
      route.CraftApp === manifest.Name ||
      (served.includes(route.Target.Host) &&
        (route.Host.startsWith(`${manifest.Name}.`) || route.Host.includes(`-${manifest.Name}.`))),
  );
}

/** The table with these routes added or replaced by host. No other route is dropped or changed. */
export function mergeRoutes(table: EdgeRoute[], add: EdgeRoute[]): EdgeRoute[] {
  const hosts = new Set(add.map((route) => route.Host));
  return [...table.filter((route) => !hosts.has(route.Host)), ...add];
}

/**
 * Removes an app's routes from the edge table through the edge routes API,
 * sending the rest of the table back unchanged. Resolves the removed hosts and
 * the reconcile operation (null when the edge refused it), or null when the app had no route.
 */
export function removeAppRoutes(
  api: ApiService,
  manifest: CatalogManifest,
): Observable<{ hosts: string[]; operation: OperationStarted | null } | null> {
  return api.edgeRoutes().pipe(
    switchMap(({ routes }) => {
      const table = routes ?? [];
      const hosts = appRoutes(manifest, table).map((route) => route.Host);
      if (hosts.length === 0) {
        return of(null);
      }
      return api
        .edgeReconcile(table.filter((route) => !hosts.includes(route.Host)))
        .pipe(
          map((operation) => ({ hosts, operation })),
          // A refused change leaves the address in the table; the caller says so.
          catchError(() => of({ hosts, operation: null })),
        );
    }),
  );
}
