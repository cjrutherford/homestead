import { Routes } from '@angular/router';
import { Status } from './status/status';
import { Claim } from './claim/claim';
import { Login } from './login/login';
import { Config } from './config/config';
import { Account } from './account/account';
import { NotFound } from './not-found/not-found';
import { gateRoutes } from './setup/setup.guard';
import { Home } from './home/home';
import { homeGuard, ownerGuard } from './home/home.guard';

/**
 * Every administration screen lives under /admin (ADR-050 §4). Rarely
 * visited pages load on demand to keep the initial bundle small.
 */
const adminRoutes: Routes = [
  { path: '', redirectTo: 'status', pathMatch: 'full' },
  { path: 'status', component: Status },
  { path: 'config', component: Config },
  { path: 'firewall', loadComponent: () => import('./firewall/firewall').then((m) => m.Firewall) },
  {
    // Loaded on demand with the wizard it shares the uninstall dialog with.
    path: 'catalog',
    loadComponent: () => import('./catalog/catalog').then((m) => m.Catalog),
  },
  {
    // Loaded on demand: the wizard and its curated copy are only needed to install.
    path: 'catalog/:name/install',
    loadComponent: () => import('./catalog/install-wizard').then((m) => m.InstallWizard),
  },
  { path: 'updates', loadComponent: () => import('./updates/updates').then((m) => m.Updates) },
  { path: 'data', loadComponent: () => import('./data/data-migration').then((m) => m.DataMigration) },
  { path: 'advanced', loadComponent: () => import('./advanced/advanced').then((m) => m.Advanced) },
  { path: 'doctor', loadComponent: () => import('./doctor/doctor').then((m) => m.Doctor) },
  { path: 'people', loadComponent: () => import('./people/people').then((m) => m.People) },
  { path: 'drift', loadComponent: () => import('./drift/drift').then((m) => m.Drift) },
  { path: 'exposure', loadComponent: () => import('./exposure/exposure').then((m) => m.Exposure) },
  { path: 'tunnel', loadComponent: () => import('./tunnel/tunnel').then((m) => m.Tunnel) },
  { path: 'names', loadComponent: () => import('./names/names').then((m) => m.Names) },
  { path: 'backups', loadComponent: () => import('./backups/backups').then((m) => m.Backups) },
  { path: 'storage', loadComponent: () => import('./storage/storage').then((m) => m.StorageSetup) },
  { path: 'edge', loadComponent: () => import('./edge/edge').then((m) => m.Edge) },
  { path: 'tls', loadComponent: () => import('./tls/tls').then((m) => m.Tls) },
  { path: 'trust', loadComponent: () => import('./trust/trust').then((m) => m.Trust) },
  { path: 'craft-apps', loadComponent: () => import('./craftapps/craft-apps').then((m) => m.CraftApps) },
  { path: 'craft-files', loadComponent: () => import('./craftfiles/craft-files').then((m) => m.CraftFiles) },
  { path: 'audit', loadComponent: () => import('./audit/audit').then((m) => m.AuditLog) },
  { path: 'secrets', loadComponent: () => import('./secrets/secrets').then((m) => m.Secrets) },
  { path: 'operations', loadComponent: () => import('./operations/operations').then((m) => m.Operations) },
  { path: 'operations/:id', loadComponent: () => import('./operations/operation-detail').then((m) => m.OperationDetail) },
  { path: 'validation-board', loadComponent: () => import('./validation/validation-board').then((m) => m.ValidationBoard) },
];

/**
 * Pre-S339 paths. Each redirects to its /admin path with its params, query
 * and fragment, so bookmarks and old links keep working.
 */
const legacyRedirects: Routes = adminRoutes
  .map((route) => route.path ?? '')
  .filter((path) => path !== '')
  .map((path) => ({ path, redirectTo: `admin/${path}`, pathMatch: 'full' as const }));

/**
 * The route table. The apps page is the root (ADR-050 §4). Claim, login,
 * setup, sign-in and account stay at the root: the server and the
 * walkthrough send people to them by path. While the post-claim
 * walkthrough holds a fresh appliance's owner (S287, ADR-034), every admin
 * route outside SETUP_EXEMPT_PATHS redirects to /setup.
 */
export const routes: Routes = [
  { path: '', component: Home, pathMatch: 'full', canActivate: [homeGuard] },
  { path: 'claim', component: Claim },
  { path: 'login', component: Login },
  { path: 'account', component: Account, canActivate: [ownerGuard] },
  {
    // Loaded on demand: only the walkthrough needs the page (S287).
    path: 'setup',
    canActivate: [ownerGuard],
    loadComponent: () => import('./setup/setup').then((m) => m.Setup),
  },
  {
    // Loaded on demand: only the owner configuring Authentik sign-in needs it (S286).
    path: 'sign-in',
    canActivate: [ownerGuard],
    loadComponent: () => import('./sso/sign-in').then((m) => m.SignIn),
  },
  { path: 'admin', canActivate: [ownerGuard], children: gateRoutes(adminRoutes) },
  ...legacyRedirects,
  { path: '**', component: NotFound },
];
