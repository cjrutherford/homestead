import { inject, Injectable } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable } from 'rxjs';

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

/** GET /api/v1/config — current desired state. */
export interface ConfigState {
  version: number;
  settings: Record<string, unknown>;
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

  config(): Observable<ConfigState> {
    return this.http.get<ConfigState>('/api/v1/config');
  }
}
