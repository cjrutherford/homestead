import { Routes } from '@angular/router';
import { Status } from './status/status';
import { Claim } from './claim/claim';
import { Login } from './login/login';
import { Config } from './config/config';
import { Account } from './account/account';
import { NotFound } from './not-found/not-found';
import { gateRoutes } from './setup/setup.guard';

// Rarely visited pages load on demand to keep the initial bundle small.
const walkthroughRoutes: Routes = [
  { path: '', redirectTo: 'status', pathMatch: 'full' },
  { path: 'status', component: Status },
  { path: 'claim', component: Claim },
  { path: 'login', component: Login },
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
  { path: 'drift', loadComponent: () => import('./drift/drift').then((m) => m.Drift) },
  { path: 'exposure', loadComponent: () => import('./exposure/exposure').then((m) => m.Exposure) },
  { path: 'tunnel', loadComponent: () => import('./tunnel/tunnel').then((m) => m.Tunnel) },
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
  { path: 'account', component: Account },
  {
    // Loaded on demand: only the walkthrough needs the page (S287).
    path: 'setup',
    loadComponent: () => import('./setup/setup').then((m) => m.Setup),
  },
  {
    // Loaded on demand: only the owner configuring Authentik sign-in needs it (S286).
    path: 'sign-in',
    loadComponent: () => import('./sso/sign-in').then((m) => m.SignIn),
  },
  // Later slices register their routes here, before the wildcard.
  { path: '**', component: NotFound },
];

/**
 * The route table. While the post-claim walkthrough holds a fresh appliance's
 * owner (S287, ADR-034), every route outside SETUP_EXEMPT_PATHS redirects to
 * /setup. The gate is this guard and nothing else.
 */
export const routes: Routes = gateRoutes(walkthroughRoutes);
