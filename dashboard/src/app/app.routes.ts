import { Routes } from '@angular/router';
import { Status } from './status/status';
import { Claim } from './claim/claim';
import { Login } from './login/login';
import { Config } from './config/config';
import { Firewall } from './firewall/firewall';
import { Operations } from './operations/operations';
import { OperationDetail } from './operations/operation-detail';
import { NotFound } from './not-found/not-found';

export const routes: Routes = [
  { path: '', redirectTo: 'status', pathMatch: 'full' },
  { path: 'status', component: Status },
  { path: 'claim', component: Claim },
  { path: 'login', component: Login },
  { path: 'config', component: Config },
  { path: 'firewall', component: Firewall },
  { path: 'operations', component: Operations },
  { path: 'operations/:id', component: OperationDetail },
  // Later slices register their routes here, before the wildcard.
  { path: '**', component: NotFound },
];
