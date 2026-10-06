import { Routes } from '@angular/router';
import { Status } from './status/status';
import { Claim } from './claim/claim';
import { Login } from './login/login';
import { NotFound } from './not-found/not-found';

export const routes: Routes = [
  { path: '', redirectTo: 'status', pathMatch: 'full' },
  { path: 'status', component: Status },
  { path: 'claim', component: Claim },
  { path: 'login', component: Login },
  // Configuration (S035) pages register their routes here, before the
  // wildcard, in their own slice.
  { path: '**', component: NotFound },
];
