import { Routes } from '@angular/router';
import { Status } from './status/status';
import { NotFound } from './not-found/not-found';

export const routes: Routes = [
  { path: '', redirectTo: 'status', pathMatch: 'full' },
  { path: 'status', component: Status },
  // Claim (S032), authentication (S033), and configuration (S035) pages
  // register their routes here, before the wildcard, in their own slices.
  { path: '**', component: NotFound },
];
