import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ApiService, LockoutStatus } from '../core/api.service';
import { ERROR_CODE_HEADER, WRONG_CURRENT_PASSWORD } from '../core/auth.interceptor';
import { StateMessage } from '../state/state-message';

/** Mirrors auth.MinPasswordLength in the daemon. */
export const MIN_PASSWORD_LENGTH = 12;

/**
 * Account surfaces (S274): own lockout status and password change,
 * matching `homesteadctl admin lockout` and `admin passwd`. The daemon
 * reports only whether the account is locked (no attempt counts or
 * unlock time), so that is all this page can show. Passwords live in the
 * form controls only and are cleared after every submit.
 */
@Component({
  imports: [StateMessage],
  selector: 'app-account',
  templateUrl: './account.html',
  styles: [
    'button, input { min-height: 44px; }',
    'input { max-width: 100%; box-sizing: border-box; }',
    'section { overflow-wrap: anywhere; }',
  ],
})
export class Account implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly minLength = MIN_PASSWORD_LENGTH;
  protected readonly lockout = signal<LockoutStatus | null>(null);
  protected readonly loading = signal(true);
  protected readonly loadError = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly problems = signal<string[]>([]);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);
  /** Bumped after each submit so the form inputs are recreated empty. */
  protected readonly formKey = signal(0);

  ngOnInit(): void {
    this.loadLockout();
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
  validate(current: string, next: string, confirm: string): string[] {
    const out: string[] = [];
    if (!current) {
      out.push('Enter your current password.');
    }
    if (next.length < MIN_PASSWORD_LENGTH) {
      out.push(`The new password must be at least ${MIN_PASSWORD_LENGTH} characters.`);
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
        this.formKey.update((n) => n + 1);
        this.notice.set(
          'Password changed. Your other sessions were signed out; this one stays signed in.',
        );
        this.loadLockout();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.formKey.update((n) => n + 1);
        this.error.set(this.describe(failure));
        this.loadLockout();
      },
    });
  }

  protected describe(failure: HttpErrorResponse): string {
    if (failure.status === 0) {
      return 'Cannot reach the Homestead daemon. Check that it is running and try again.';
    }
    if (failure.status === 401 && failure.headers?.get(ERROR_CODE_HEADER) === WRONG_CURRENT_PASSWORD) {
      return 'The current password is incorrect. Repeated failures lock the account temporarily.';
    }
    if (failure.status === 429) {
      return 'The account is temporarily locked after repeated failures. Try again later.';
    }
    const body = failure.error;
    if (typeof body === 'string' && body.trim()) {
      return body.trim();
    }
    return `The daemon answered ${failure.status}.`;
  }
}
