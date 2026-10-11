import { computed, inject, Injectable, signal } from '@angular/core';
import { catchError, map, Observable, of, tap } from 'rxjs';
import { ApiService } from '../core/api.service';

/**
 * Display-only session hint. The credential is the HttpOnly cookie the
 * browser holds; no token is ever readable by, or stored by, the
 * dashboard (ADR-020, S256).
 */
export interface StoredSession {
  username: string;
  expiresAt: string;
}

/** localStorage key for the client session (S033). */
export const SESSION_STORAGE_KEY = 'homestead.session';

/**
 * Client session state (S033, S256): the logged-in user, persisted across
 * reloads as a hint; the server session lives in the cookie (12h idle, 7d
 * absolute). Logout clears locally first and revokes server-side
 * best-effort. A 401 from the API calls expire().
 */
@Injectable({ providedIn: 'root' })
export class SessionService {
  private readonly api = inject(ApiService);

  private readonly session = signal<StoredSession | null>(readStoredSession());

  readonly username = computed(() => this.session()?.username ?? null);
  readonly isLoggedIn = computed(() => this.session() !== null);

  login(username: string, password: string): Observable<void> {
    return this.api.login(username, password).pipe(
      tap((session) => {
        this.store({
          username,
          expiresAt: session.expires_at,
        });
      }),
      map(() => undefined),
    );
  }

  logout(): Observable<void> {
    const wasLoggedIn = this.isLoggedIn();
    this.clear();
    if (!wasLoggedIn) {
      return of(undefined);
    }
    return this.api.logout().pipe(
      map(() => undefined),
      catchError(() => of(undefined)),
    );
  }

  /** Drops the local hint after the server said the session is gone. */
  expire(): void {
    this.clear();
  }

  private store(session: StoredSession): void {
    this.session.set(session);
    try {
      localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify(session));
    } catch {
      // Private browsing etc: in-memory session still works for the tab.
    }
  }

  private clear(): void {
    this.session.set(null);
    try {
      localStorage.removeItem(SESSION_STORAGE_KEY);
    } catch {
      // Already in-memory only; nothing to remove.
    }
  }
}

function readStoredSession(): StoredSession | null {
  try {
    const raw = localStorage.getItem(SESSION_STORAGE_KEY);
    if (!raw) {
      return null;
    }
    const parsed = JSON.parse(raw) as Partial<StoredSession> & { token?: unknown };
    if (typeof parsed.username !== 'string' || !parsed.username) {
      localStorage.removeItem(SESSION_STORAGE_KEY); // unusable, maybe pre-S256
      return null;
    }
    const hint: StoredSession = {
      username: parsed.username,
      expiresAt: typeof parsed.expiresAt === 'string' ? parsed.expiresAt : '',
    };
    if ('token' in parsed) {
      // Pre-S256 entries kept the bearer token in localStorage. Rewrite
      // the entry as a display hint so the credential does not linger.
      localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify(hint));
    }
    return hint;
  } catch {
    return null;
  }
}
