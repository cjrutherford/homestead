import { computed, inject, Injectable, signal } from '@angular/core';
import { catchError, map, Observable, of, tap } from 'rxjs';
import { ApiService } from '../core/api.service';

export interface StoredSession {
  token: string;
  username: string;
  expiresAt: string;
}

/** localStorage key for the client session (S033). */
export const SESSION_STORAGE_KEY = 'homestead.session';

/**
 * Client session state (S033): logged-in user plus token, persisted across
 * browser restarts to match the 30-day server sessions. Logout clears
 * locally first and destroys server-side best-effort.
 */
@Injectable({ providedIn: 'root' })
export class SessionService {
  private readonly api = inject(ApiService);

  private readonly session = signal<StoredSession | null>(readStoredSession());

  readonly token = computed(() => this.session()?.token ?? null);
  readonly username = computed(() => this.session()?.username ?? null);
  readonly isLoggedIn = computed(() => this.session() !== null);

  login(username: string, password: string): Observable<void> {
    return this.api.login(username, password).pipe(
      tap((session) => {
        this.store({
          token: session.token,
          username,
          expiresAt: session.expires_at,
        });
      }),
      map(() => undefined),
    );
  }

  logout(): Observable<void> {
    const token = this.token();
    this.clear();
    if (!token) {
      return of(undefined);
    }
    return this.api.logout(token).pipe(
      map(() => undefined),
      catchError(() => of(undefined)),
    );
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
    const parsed = JSON.parse(raw) as Partial<StoredSession>;
    if (typeof parsed.token !== 'string' || !parsed.token) {
      return null;
    }
    return {
      token: parsed.token,
      username: typeof parsed.username === 'string' ? parsed.username : '',
      expiresAt: typeof parsed.expiresAt === 'string' ? parsed.expiresAt : '',
    };
  } catch {
    return null;
  }
}
