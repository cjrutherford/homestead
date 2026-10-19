import { NgComponentOutlet } from '@angular/common';
import { Component, inject, OnInit, signal, Type } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { SessionService } from '../auth/session.service';
import { ApiService, LockoutStatus } from '../core/api.service';
import { ERROR_CODE_HEADER, WRONG_CURRENT_PASSWORD } from '../core/auth.interceptor';
import { StateMessage } from '../state/state-message';
import { THEME_PREFERENCES, ThemePreference, ThemeService } from '../core/theme.service';
import { Loading } from '../ui/loading';

/** Mirrors auth.MinPasswordLength and MaxPasswordLength (both in bytes). */
export const MIN_PASSWORD_LENGTH = 12;
export const MAX_PASSWORD_LENGTH = 72;

const utf8 = new TextEncoder();
/** The daemon counts UTF-8 bytes, not characters. */
export function byteLength(value: string): number {
  return utf8.encode(value).length;
}

/** "in 4 minutes" style wording for a future instant. */
export function relativeUntil(iso: string, now: Date = new Date()): string {
  const ms = Date.parse(iso) - now.getTime();
  if (Number.isNaN(ms)) {
    return 'at an unknown time';
  }
  if (ms <= 0) {
    return 'any moment now';
  }
  const minutes = Math.ceil(ms / 60000);
  if (minutes < 60) {
    return `in ${minutes} minute${minutes === 1 ? '' : 's'}`;
  }
  const hours = Math.ceil(minutes / 60);
  return `in ${hours} hour${hours === 1 ? '' : 's'}`;
}

/**
 * Account surfaces (S274): own lockout status and password change,
 * matching `homesteadctl admin lockout` and `admin passwd`. The daemon
 * reports only whether the account is locked (no attempt counts or
 * unlock time), so that is all this page can show. Passwords live in the
 * form controls only and are cleared after every submit.
 */
@Component({
  imports: [Loading, NgComponentOutlet, StateMessage],
  selector: 'app-account',
  templateUrl: './account.html',
  styles: ['section { overflow-wrap: anywhere; }'],
})
export class Account implements OnInit {
  private readonly api = inject(ApiService);
  protected readonly sessions = inject(SessionService);
  protected readonly theme = inject(ThemeService);
  protected readonly themeOptions = THEME_PREFERENCES;

  protected readonly minLength = MIN_PASSWORD_LENGTH;
  protected readonly maxLength = MAX_PASSWORD_LENGTH;
  protected readonly relativeUntil = relativeUntil;
  protected readonly lockout = signal<LockoutStatus | null>(null);
  protected readonly loading = signal(true);
  protected readonly loadError = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly problems = signal<string[]>([]);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);

  /** The Authentik link card, loaded on demand (S286): most visits never need it. */
  protected readonly ssoCard = signal<Type<unknown> | null>(null);

  ngOnInit(): void {
    void import('../sso/sso-link').then((m) => this.ssoCard.set(m.SsoLink));
    this.loadLockout();
  }

  protected chooseTheme(preference: ThemePreference): void {
    this.theme.set(preference);
  }

  loadLockout(): void {
    this.loading.set(true);
    this.loadError.set(null);
    this.api.lockoutStatus().subscribe({
      next: (status) => {
        this.loading.set(false);
        this.lockout.set(status);
      },
      error: (failure: HttpErrorResponse) => {
        this.loading.set(false);
        this.loadError.set(`Could not load lockout status: ${this.describe(failure)}`);
      },
    });
  }

  /** Client rules mirror the daemon: non-empty, at least 12 characters. */
  /** The problems that belong to one field, so each shows under its input. */
  protected problemsFor(field: 'current' | 'new' | 'confirm'): string[] {
    const matches: Record<typeof field, RegExp> = {
      current: /current password\.$/,
      new: /^The new password must/,
      confirm: /do not match/,
    };
    return this.problems().filter((problem) => matches[field].test(problem));
  }

  validate(current: string, next: string, confirm: string): string[] {
    const out: string[] = [];
    if (!current) {
      out.push('Enter your current password.');
    }
    const bytes = byteLength(next);
    if (bytes < MIN_PASSWORD_LENGTH) {
      out.push(`The new password must be at least ${MIN_PASSWORD_LENGTH} bytes (letters, digits and symbols are one byte each).`);
    }
    if (bytes > MAX_PASSWORD_LENGTH) {
      out.push(`The new password must be at most ${MAX_PASSWORD_LENGTH} bytes (accented and non-Latin characters use two or more bytes each).`);
    }
    if (next !== confirm) {
      out.push('The new password and its confirmation do not match.');
    }
    return out;
  }

  submit(current: string, next: string, confirm: string): void {
    this.error.set(null);
    this.notice.set(null);
    const problems = this.validate(current, next, confirm);
    this.problems.set(problems);
    if (problems.length > 0) {
      return;
    }
    this.busy.set(true);
    this.api.changePassword(current, next).subscribe({
      next: () => {
        this.busy.set(false);
        this.notice.set(
          'Password changed. Your other sessions were signed out; this one stays signed in.',
        );
        this.loadLockout();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(this.describe(failure));
        this.loadLockout();
      },
    });
  }

  protected describe(failure: HttpErrorResponse): string {
    if (failure.status === 0) {
      return "Can't reach the appliance. Check that homestead-server is running.";
    }
    if (failure.status === 401 && failure.headers?.get(ERROR_CODE_HEADER) === WRONG_CURRENT_PASSWORD) {
      return 'The current password is incorrect. Repeated failures lock the account temporarily.';
    }
    if (failure.status === 422) {
      const body = failure.error;
      return typeof body === 'string' && body.trim() ? body.trim() : 'The new password was refused.';
    }
    if (failure.status === 429) {
      return 'The account is temporarily locked after repeated failures. Try again later.';
    }
    const body = failure.error;
    if (typeof body === 'string' && body.trim()) {
      return body.trim();
    }
    return `The appliance answered ${failure.status}. Try again, and check its log if it keeps happening.`;
  }
}
