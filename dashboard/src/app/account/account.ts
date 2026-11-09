import { NgComponentOutlet } from '@angular/common';
import { Component, inject, OnInit, signal, Type } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ApiService, LockoutStatus } from '../core/api.service';
import { PasswordForm, describeAccountFailure } from './password-form';
import { StateMessage } from '../state/state-message';
import { THEME_PREFERENCES, ThemePreference, ThemeService } from '../core/theme.service';
import { Loading } from '../ui/loading';

export { byteLength, MIN_PASSWORD_LENGTH, MAX_PASSWORD_LENGTH } from './password-form';

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
  imports: [Loading, NgComponentOutlet, PasswordForm, StateMessage],
  selector: 'app-account',
  templateUrl: './account.html',
  styles: ['section { overflow-wrap: anywhere; }'],
})
export class Account implements OnInit {
  private readonly api = inject(ApiService);
  protected readonly theme = inject(ThemeService);
  protected readonly themeOptions = THEME_PREFERENCES;

  protected readonly relativeUntil = relativeUntil;
  protected readonly lockout = signal<LockoutStatus | null>(null);
  protected readonly loading = signal(true);
  protected readonly loadError = signal<string | null>(null);

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

  protected describe(failure: HttpErrorResponse): string {
    return describeAccountFailure(failure);
  }
}
