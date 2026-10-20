import { HttpClient } from '@angular/common/http';
import { Injectable, computed, inject, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { Observable, tap } from 'rxjs';
import { httpErrorMessage } from '../core/http-error';
import { ToastService } from '../core/toast.service';

/** Step names, in order (ADR-036). */
export type SetupStepName = 'account' | 'storage' | 'sign-in' | 'domain' | 'apps';
export type SetupStepState = 'pending' | 'done' | 'skipped';

export interface SetupStep {
  name: SetupStepName;
  title: string;
  state: SetupStepState;
  detail: string;
  skipped: boolean;
  /** Setup finished before this step existed: not required, and not an owner skip. */
  waived: boolean;
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
    storage_app_assigned: boolean;
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
 * Setup wizard state (S287, ADR-034; S291, ADR-036). Besides the two API calls it
 * keeps the last status, which the router guard and the navigation read.
 * The gate is a dashboard convenience only: the API and CLI never read it.
 */
@Injectable({ providedIn: 'root' })
export class SetupService {
  private readonly http = inject(HttpClient);
  private readonly toast = inject(ToastService);

  /** True while a step change is in flight. */
  readonly busy = signal(false);
  /** The last step change that failed, in words; cleared by the next one. */
  readonly error = signal<string | null>(null);
  /**
   * Bumped whenever something the wizard's status derives from may have
   * changed (a successful write on another route, see setupRefreshInterceptor).
   * The wizard re-reads the status when it moves.
   */
  readonly changed = signal(0);

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

  /** Tells the wizard that something its status depends on may have changed. */
  touch(): void {
    this.changed.update((n) => n + 1);
  }

  /** A step change with the busy flag, the error text and an optional toast, shared by the steps. */
  change(name: SetupStepName, change: SetupStepChange, message?: string, after?: () => void): void {
    if (this.busy()) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.setupStep(name, change).subscribe({
      next: () => {
        this.busy.set(false);
        if (message) {
          this.toast.show(message);
        }
        after?.();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(httpErrorMessage(failure, 'setup'));
      },
    });
  }

  /** Forgets the cached status (logout, or a factory reset). */
  clear(): void {
    this.status.set(null);
  }
}
