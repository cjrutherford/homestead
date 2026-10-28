import { HttpClient } from '@angular/common/http';
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

/** One app a person can open; role is user or admin (the app's own admin). */
export interface PersonGrant {
  app: string;
  role: 'user' | 'admin';
}

export interface PersonDrift {
  kind: 'field' | 'grant';
  field?: string;
  app?: string;
  homestead: string;
  authentik: string;
}

export interface Person {
  id: number;
  display_name: string;
  username: string;
  email: string;
  state: 'active' | 'disabled';
  origin: 'created' | 'adopted';
  grants: PersonGrant[];
  /** ok, drifted, missing or unchecked. */
  status: 'ok' | 'drifted' | 'missing' | 'unchecked';
  drift: PersonDrift[] | null;
}

export interface UnmanagedUser {
  username: string;
  name: string;
  email: string;
  active: boolean;
}

export interface PeopleView {
  token_kept: boolean;
  people: Person[] | null;
  unmanaged: UnmanagedUser[] | null;
  apps: string[] | null;
  read_error?: string;
  owner: string;
  /** no-token or no-sign-in when Homestead cannot act. */
  reason?: 'no-token' | 'no-sign-in';
  token_words: string;
}

/** A credential shown once: a password or a one-time sign-in link. */
export interface Revealed {
  kind: 'password' | 'link';
  value: string;
}

/** What adding a person returns; sign_in_failed: they were added but how they sign in was not set up. */
export interface PersonAdded {
  person: Person;
  revealed: Revealed | null;
  warning?: string;
  sign_in_failed?: boolean;
}

export interface PersonAdd {
  display_name: string;
  username: string;
  email: string;
  method?: 'link' | 'generated';
  password?: string;
  grants?: PersonGrant[];
}

export interface PersonEdit {
  display_name?: string;
  username?: string;
  email?: string;
}

export type DriftAction = 'put-back' | 'keep' | 'recreate' | 'forget';

/**
 * People (S317, ADR-043): the same changes as `homesteadctl people`. Every
 * change carries `confirm`; step-up routes ask for the local password
 * through the re-authentication interceptor for Authentik sessions.
 */
@Injectable({ providedIn: 'root' })
export class PeopleService {
  private readonly http = inject(HttpClient);

  people(): Observable<PeopleView> {
    return this.http.get<PeopleView>('/api/v1/people');
  }

  addPerson(body: PersonAdd): Observable<PersonAdded> {
    return this.http.post<PersonAdded>('/api/v1/people', { ...body, confirm: true });
  }

  adoptPerson(username: string): Observable<{ person: Person }> {
    return this.http.post<{ person: Person }>('/api/v1/people/adopt', { username, confirm: true });
  }

  editPerson(id: number, body: PersonEdit): Observable<{ person: Person }> {
    return this.http.patch<{ person: Person }>(`/api/v1/people/${id}`, { ...body, confirm: true });
  }

  resetPassword(id: number, method: 'link' | 'generated', password = ''): Observable<{ revealed: Revealed | null }> {
    return this.http.post<{ revealed: Revealed | null }>(`/api/v1/people/${id}/password-reset`, { method, password, confirm: true });
  }

  resetSecondFactor(id: number): Observable<{ removed: string[]; skipped: string[] }> {
    return this.http.post<{ removed: string[]; skipped: string[] }>(`/api/v1/people/${id}/mfa-reset`, { confirm: true });
  }

  disablePerson(id: number): Observable<unknown> {
    return this.http.post(`/api/v1/people/${id}/disable`, { confirm: true });
  }

  enablePerson(id: number): Observable<unknown> {
    return this.http.post(`/api/v1/people/${id}/enable`, { confirm: true });
  }

  removePerson(id: number): Observable<unknown> {
    return this.http.post(`/api/v1/people/${id}/remove`, { confirm: true });
  }

  grantApp(id: number, app: string, admin: boolean): Observable<{ person: Person }> {
    const path = `/api/v1/people/${id}/apps/${encodeURIComponent(app)}${admin ? '/admin' : ''}`;
    return this.http.put<{ person: Person }>(path, { confirm: true });
  }

  revokeApp(id: number, app: string): Observable<{ person: Person }> {
    return this.http.post<{ person: Person }>(`/api/v1/people/${id}/apps/${encodeURIComponent(app)}/revoke`, { confirm: true });
  }

  resolveDrift(id: number, action: DriftAction, method: 'link' | 'generated' = 'link'): Observable<{ revealed: Revealed | null }> {
    return this.http.post<{ revealed: Revealed | null }>(`/api/v1/people/${id}/resolve`, { action, method, confirm: true });
  }
}
