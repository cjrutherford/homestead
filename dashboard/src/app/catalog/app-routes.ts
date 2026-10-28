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

/**
 * The address an installed app would have been given: local only, internal TLS,
 * the same shape the install plan produces. Used when the app is installed and
 * the table has no route for it (the wizard was closed mid-install, or the CLI
 * installed it). Public exposure is never chosen here (ADR-006).
 *
 * A package served by path (ADR-044, and every craft app, ADR-045 §1) gets
 * its path, prefix strip and redirects, as the install plan gives them:
 * before S326 this sent a craft app's route on its own name, which is how
 * lightcraft answered at lightcraft.lan.
 */
export function routeFromManifest(manifest: CatalogManifest, host: string, port: number): EdgeRoute | null {
  const service = manifest.Services.find((entry) => entry.Exposes !== '');
  if (!service) {
    return null;
  }
  const recommended = manifest.Exposure.Endpoints.find((entry) => entry.Endpoint === service.Exposes)?.Recommended;
  return {
    Host: host,
    Target: { Host: service.Name, Port: port },
    Exposure: recommended === 'overlay' ? 'overlay' : 'local',
    TLS: 'internal',
    Health: { Path: '', IntervalSeconds: 0 },
    ...(manifest.Services.some((entry) => entry.Image.startsWith('homestead/craft-')) ? { CraftApp: manifest.Name } : {}),
    ...(manifest.Serve === 'path'
      ? {
          Path: manifest.Path || `/apps/${manifest.Name}/`,
          StripPrefix: !!manifest.StripPrefix,
          Redirects: manifest.Redirects ?? null,
        }
      : {}),
  };
}
