import { Component, inject, output, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { SessionService } from '../auth/session.service';
import { ApiService } from '../core/api.service';
import { ERROR_CODE_HEADER, WRONG_CURRENT_PASSWORD } from '../core/auth.interceptor';
import { StateMessage } from '../state/state-message';

/** Mirrors auth.MinPasswordLength and MaxPasswordLength (both in bytes). */
export const MIN_PASSWORD_LENGTH = 12;
export const MAX_PASSWORD_LENGTH = 72;

const utf8 = new TextEncoder();
/** The daemon counts UTF-8 bytes, not characters. */
export function byteLength(value: string): number {
  return utf8.encode(value).length;
}

type PasswordField = 'current' | 'new' | 'confirm';

interface PasswordProblem {
  field: PasswordField;
  message: string;
}


/**
 * The change-password form, shared by the Account page and the setup wizard's
 * account step so both call the same route with the same rules. Passwords live
 * in the form controls only and are cleared after every submit.
 */
@Component({
  imports: [StateMessage],
  selector: 'app-password-form',
  templateUrl: './password-form.html',
})
export class PasswordForm {
  private readonly api = inject(ApiService);
  protected readonly sessions = inject(SessionService);

  /** Emitted after a try, successful or not, so a host can re-read what depends on it (lockout). */
  readonly attempted = output<void>();
  /** Emitted only after the password was changed. */
  readonly changed = output<void>();

  protected readonly minLength = MIN_PASSWORD_LENGTH;
  protected readonly maxLength = MAX_PASSWORD_LENGTH;
  protected readonly busy = signal(false);
  protected readonly problems = signal<PasswordProblem[]>([]);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);

  /** The problems that belong to one field, so each shows under its input. */
  protected problemsFor(field: PasswordField): string[] {
    return this.problems()
      .filter((problem) => problem.field === field)
      .map((problem) => problem.message);
  }

  /** Client rules mirror the daemon: non-empty, at least 12 characters. */
  validate(current: string, next: string, confirm: string): string[] {
    return this.check(current, next, confirm).map((problem) => problem.message);
  }

  /** Each problem carries the field it belongs to. */
  private check(current: string, next: string, confirm: string): PasswordProblem[] {
    const out: PasswordProblem[] = [];
    if (!current) {
      out.push({ field: 'current', message: 'Enter your current password.' });
    }
    const bytes = byteLength(next);
    if (bytes < MIN_PASSWORD_LENGTH) {
      out.push({
        field: 'new',
        message: `The new password must be at least ${MIN_PASSWORD_LENGTH} bytes (letters, digits and symbols are one byte each).`,
      });
    }
    if (bytes > MAX_PASSWORD_LENGTH) {
      out.push({
        field: 'new',
        message: `The new password must be at most ${MAX_PASSWORD_LENGTH} bytes (accented and non-Latin characters use two or more bytes each).`,
      });
    }
    if (next !== confirm) {
      out.push({ field: 'confirm', message: 'The new password and its confirmation do not match.' });
    }
    return out;
  }

  submit(current: string, next: string, confirm: string): void {
    this.error.set(null);
    this.notice.set(null);
    const problems = this.check(current, next, confirm);
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
        this.attempted.emit();
        this.changed.emit();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(describeAccountFailure(failure));
        this.attempted.emit();
      },
    });
  }
}

export function describeAccountFailure(failure: HttpErrorResponse): string {
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
