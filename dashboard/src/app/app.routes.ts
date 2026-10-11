import { Routes } from '@angular/router';
import { Status } from './status/status';
import { Claim } from './claim/claim';
import { Login } from './login/login';
import { Config } from './config/config';
import { Firewall } from './firewall/firewall';
import { Catalog } from './catalog/catalog';
import { Updates } from './updates/updates';
import { Advanced } from './advanced/advanced';
import { Drift } from './drift/drift';
import { Exposure } from './exposure/exposure';
import { Tunnel } from './tunnel/tunnel';
import { Secrets } from './secrets/secrets';
import { Operations } from './operations/operations';
import { OperationDetail } from './operations/operation-detail';
import { ValidationBoard } from './validation/validation-board';
import { Backups } from './backups/backups';
import { NotFound } from './not-found/not-found';

export const routes: Routes = [
  { path: '', redirectTo: 'status', pathMatch: 'full' },
  { path: 'status', component: Status },
  { path: 'claim', component: Claim },
  { path: 'login', component: Login },
  { path: 'config', component: Config },
  { path: 'firewall', component: Firewall },
  { path: 'catalog', component: Catalog },
  { path: 'updates', component: Updates },
  { path: 'advanced', component: Advanced },
  { path: 'drift', component: Drift },
  { path: 'exposure', component: Exposure },
  { path: 'tunnel', component: Tunnel },
  { path: 'backups', component: Backups },
  { path: 'secrets', component: Secrets },
  { path: 'operations', component: Operations },
  { path: 'operations/:id', component: OperationDetail },
  { path: 'validation-board', component: ValidationBoard },
  // Later slices register their routes here, before the wildcard.
  { path: '**', component: NotFound },
];
