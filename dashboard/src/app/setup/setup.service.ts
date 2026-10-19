import { HttpClient } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { Observable, tap } from 'rxjs';

/** Step names, in order (ADR-034). */
export type SetupStepName = 'account' | 'sign-in' | 'domain' | 'apps';
export type SetupStepState = 'pending' | 'done' | 'skipped';

export interface SetupStep {
  name: SetupStepName;
  title: string;
  state: SetupStepState;
  detail: string;
  skipped: boolean;
  acknowledged: boolean;
}

export interface SetupPermission {
  grant: string;
  reason: string;
}

export interface SetupProvider {
  name: string;
  label: string;
  summary: string;
  credentials: { name: string; label: string; secret: boolean }[] | null;
  permissions: SetupPermission[] | null;
  tunnel_permissions: SetupPermission[] | null;
  notes: string[] | null;
}

export interface SetupAccess {
  id: 'home' | 'tunnel' | 'router';
  label: string;
  description: string;
  available: boolean;
  reason?: string;
}

export interface SetupStarterApp {
  name: string;
  title: string;
  blurb: string;
  installed: boolean;
}

/** GET /api/v1/setup. Holds no credential: only names, presence and choices. */
export interface SetupStatus {
  origin: 'fresh' | 'legacy';
  legacy: boolean;
  complete: boolean;
  /** Input of the router guard only; no API route reads it. */
  gated: boolean;
  current: SetupStepName | '';
  steps: SetupStep[];
  choices: { provider?: string; access?: string };
  facts: {
    recovery_exported: boolean;
    recovery_acknowledged: boolean;
    password_confirmed: boolean;
    sign_in: { authentik_installed: boolean; configured: boolean; linked: boolean; dormant: boolean };
    domain: { configured: boolean; provider?: string; zone?: string; onboarded: boolean; credentials_sealed: boolean };
    tunnels: number;
    exposures: number;
    review_existing: boolean;
    apps_installed: number;
  };
  providers: SetupProvider[];
  access: SetupAccess[];
  starter_apps: SetupStarterApp[];
}

export interface SetupStepChange {
  state?: SetupStepState;
  provider?: string;
  access?: string;
}

/**
 * Setup walkthrough state (S287, ADR-034). Besides the two API calls it
 * keeps the last status, which the router guard and the navigation read.
 * The gate is a dashboard convenience only: the API and CLI never read it.
 */
@Injectable({ providedIn: 'root' })
export class SetupService {
  private readonly http = inject(HttpClient);

  /** The last status read, or null before the first read or when it failed. */
  readonly status = signal<SetupStatus | null>(null);
  /** The label of the navigation entry: null while the walkthrough is not offered. */
  readonly navLabel = computed(() => {
    const status = this.status();
    if (status === null) {
      return null;
    }
    return status.legacy && !status.complete ? 'Finish setup' : 'Setup';
  });

  setupStatus(): Observable<SetupStatus> {
    return this.http.get<SetupStatus>('/api/v1/setup').pipe(tap((status) => this.status.set(status)));
  }

  setupStep(name: SetupStepName, change: SetupStepChange): Observable<SetupStatus> {
    return this.http
      .put<SetupStatus>(`/api/v1/setup/steps/${encodeURIComponent(name)}`, change)
      .pipe(tap((status) => this.status.set(status)));
  }

  /** Forgets the cached status (logout, or a factory reset). */
  clear(): void {
    this.status.set(null);
  }
}
