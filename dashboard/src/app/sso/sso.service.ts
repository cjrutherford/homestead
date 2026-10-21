import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

/** What a plan will make in Authentik (S286, ADR-033). Holds no secret. */
export interface SsoSpec {
  authentik_url: string;
  issuer: string;
  client_id: string;
  origins: string[];
  redirect_uris: string[];
  authorization_flow: string;
  invalidation_flow: string;
  signing_key: string;
  scopes: string[];
  authentik_version: string;
  /** Dashboard origins the tunnel delivers (S296); they sign in at public_authentik_url. */
  public_origins?: string[];
  public_authentik_url?: string;
}

export interface SsoObject {
  kind: string;
  name: string;
  action: string;
  changes?: string[];
}

export interface SsoPlan {
  digest: string;
  spec: SsoSpec;
  objects: SsoObject[];
  untouched: string[];
  warnings: string[];
  inspected: boolean;
  expires_at: string;
}

export interface SsoPlanRequest {
  authentik_url?: string;
  origins?: string[];
  signing_key?: string;
  api_token?: string;
}

export interface SsoState {
  state: 'none' | 'planned' | 'approved' | 'configured';
  configured: boolean;
  linked: boolean;
  dormant: boolean;
  offered: boolean;
  spec?: SsoSpec;
  subject?: string;
  linked_at?: string;
  client_secret_sealed: boolean;
  api_token_kept: boolean;
  plan?: { digest: string; approved: boolean; expires_at: string };
  defaults: {
    authentik_url: string;
    origins: string[] | null;
    public_origins?: string[];
    public_authentik_url?: string;
  };
  authentik_installed: boolean;
  session_method: string;
  /** The tunnel added a public dashboard name the setup does not cover yet (S296). */
  replan?: boolean;
  replan_reason?: string;
}

/**
 * Sign-in with Authentik (S286, ADR-033): the configuration plan flow, link
 * and unlink. Link, unlink and the step-up routes ask for the local password
 * through the re-authentication interceptor, so none of these methods take
 * one. The public `ssoOptions` call lives in ApiService because the login
 * page needs it before this chunk is worth loading.
 */
@Injectable({ providedIn: 'root' })
export class SsoService {
  private readonly http = inject(HttpClient);

  ssoStatus(): Observable<SsoState> {
    return this.http.get<SsoState>('/api/v1/sso');
  }

  ssoPlan(body: SsoPlanRequest): Observable<SsoPlan> {
    return this.http.post<SsoPlan>('/api/v1/sso/plan', body);
  }

  ssoApprove(body: { digest: string; api_token?: string; keep_token: boolean }): Observable<SsoState> {
    return this.http.post<SsoState>('/api/v1/sso/approve', body);
  }

  ssoApply(): Observable<{ id: number }> {
    return this.http.post<{ id: number }>('/api/v1/sso/apply', {});
  }

  ssoLink(origin?: string): Observable<{ url: string; expires_at: string }> {
    return this.http.post<{ url: string; expires_at: string }>('/api/v1/sso/link', origin ? { origin } : {});
  }

  ssoUnlink(): Observable<SsoState> {
    return this.http.delete<SsoState>('/api/v1/sso/link');
  }

  ssoDisable(): Observable<SsoState> {
    return this.http.delete<SsoState>('/api/v1/sso');
  }
}
