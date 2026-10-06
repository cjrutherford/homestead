import { inject, Injectable } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { Observable } from 'rxjs';
import { ConfigField, FieldError } from '../schema/schema';

/** GET /healthz — always available, even UNCLAIMED. */
export interface Health {
  status: string;
  version: string;
}

/** GET /api/v1/identity — claim state plus daemon version. */
export interface Identity {
  state: string;
  version: string;
}

/** GET /api/v1/doctor — diagnostic report check entry. */
export interface DoctorCheck {
  name: string;
  status: string;
  detail: string;
}

/** GET /api/v1/doctor — diagnostic report. */
export interface DoctorReport {
  overall: string;
  checks: DoctorCheck[];
}

/** GET /api/v1/config — current desired state. */
export interface ConfigState {
  version: number;
  settings: Record<string, unknown>;
}

/** POST /api/v1/config/plan and PUT /api/v1/config outcomes. */
export interface PlanResult {
  valid: boolean;
  effective?: Record<string, unknown>;
  errors?: FieldError[];
}

/** GET /api/v1/wizards/:id — saved wizard progress. */
export interface WizardState {
  wizard: string;
  step: string;
  payload: Record<string, unknown>;
  updated_at: string;
}

/** GET /api/v1/config/schema — field metadata for the renderer. */
export interface ConfigSchema {
  fields: ConfigField[];
}

/** POST /api/v1/claim — token redemption result. */
export interface ClaimResult {
  state: string;
}

/** POST /api/v1/admins/setup — created administrator. */
export interface AdminCreated {
  id: number;
  username: string;
}

/** POST /api/v1/sessions — minted session. */
export interface LoginSession {
  token: string;
  expires_at: string;
}

/**
 * Typed access to the homestead-server API (S016/S019/S020).
 * Relative URLs: production serves the dashboard from the daemon (S031),
 * development proxies /healthz, /version and /api to it (proxy.conf.json).
 */
@Injectable({ providedIn: 'root' })
export class ApiService {
  private readonly http = inject(HttpClient);

  health(): Observable<Health> {
    return this.http.get<Health>('/healthz');
  }

  identity(): Observable<Identity> {
    return this.http.get<Identity>('/api/v1/identity');
  }

  doctor(): Observable<DoctorReport> {
    return this.http.get<DoctorReport>('/api/v1/doctor');
  }

  config(): Observable<ConfigState> {
    return this.http.get<ConfigState>('/api/v1/config');
  }

  configSchema(): Observable<ConfigSchema> {
    return this.http.get<ConfigSchema>('/api/v1/config/schema');
  }

  planConfig(settings: Record<string, unknown>): Observable<PlanResult> {
    return this.http.post<PlanResult>('/api/v1/config/plan', { settings });
  }

  applyConfig(settings: Record<string, unknown>): Observable<ConfigState> {
    return this.http.put<ConfigState>('/api/v1/config', { settings });
  }

  wizardState(id: string): Observable<WizardState> {
    return this.http.get<WizardState>(`/api/v1/wizards/${id}`);
  }

  saveWizardState(
    id: string,
    step: string,
    payload: Record<string, unknown> = {},
  ): Observable<WizardState> {
    return this.http.put<WizardState>(`/api/v1/wizards/${id}`, { step, payload });
  }

  clearWizardState(id: string): Observable<void> {
    return this.http.delete<void>(`/api/v1/wizards/${id}`);
  }

  claim(token: string): Observable<ClaimResult> {
    return this.http.post<ClaimResult>('/api/v1/claim', { token });
  }

  setupAdmin(username: string, password: string): Observable<AdminCreated> {
    return this.http.post<AdminCreated>('/api/v1/admins/setup', {
      username,
      password,
    });
  }

  login(username: string, password: string): Observable<LoginSession> {
    return this.http.post<LoginSession>('/api/v1/sessions', {
      username,
      password,
    });
  }

  logout(token: string): Observable<void> {
    return this.http.delete<void>('/api/v1/sessions', {
      headers: new HttpHeaders({ Authorization: `Bearer ${token}` }),
    });
  }
}
