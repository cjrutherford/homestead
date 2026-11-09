import { Component, OnInit, inject, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { httpErrorMessage } from '../core/http-error';
import { REAUTH_INVALID, REAUTH_REQUIRED } from '../core/reauth';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { SsoService, SsoState } from './sso.service';

/** Wording for a failed link or unlink; a cancelled password prompt is not an error. */
export function linkErrorMessage(err: HttpErrorResponse, action: string): string | null {
  const code = err.headers?.get('X-Homestead-Error');
  if (err.status === 403 && (code === REAUTH_REQUIRED || code === REAUTH_INVALID)) {
    return null;
  }
  if (err.status === 429) {
    return 'The account is locked after repeated wrong passwords. Wait a while and try again.';
  }
  if (err.status === 502) {
    return 'Authentik did not answer. Check that it is running, then try again.';
  }
  const body = typeof err.error === 'string' ? err.error.trim() : '';
  return httpErrorMessage(err, action, body && err.status === 409 ? { 409: body } : {});
}

/**
 * The link between the owner account and an Authentik account (S286).
 * Shown on the Account page and the Sign-in page. Link and unlink ask for the
 * local password in the shell's dialog; unlinking signs out sessions that
 * were started with Authentik.
 */
@Component({
  selector: 'app-sso-link',
  imports: [Dialog, Loading, RouterLink, StateMessage],
  template: `
    <app-loading [loading]="loading()" shape="lines">
      @if (error()) {
        <app-state-message kind="error" [headline]="error()" />
      }
      @if (state(); as s) {
        @if (!s.configured) {
          <p>Sign-in with Authentik is not set up. <a routerLink="/sign-in">Set it up</a> first.</p>
        } @else if (s.linked) {
          <p>
            Linked to the Authentik account <code>{{ short(s.subject) }}</code>
            @if (s.dormant) {
              (dormant: Authentik is not installed)
            }.
            Your local password still works whether or not Authentik is running.
          </p>
          <p>Unlinking signs out sessions that were started with Authentik.</p>
          <button type="button" class="danger" [disabled]="busy()" [attr.aria-busy]="busy() ? 'true' : null" (click)="askUnlink()">
            Unlink Authentik
          </button>
          @if (confirmingUnlink()) {
            <app-dialog heading="Unlink Authentik?" variant="danger" (dismissed)="cancelUnlink()">
              <p>
                This removes the tie between your owner account and the Authentik account. Anyone
                signed in through Authentik is signed out, and Authentik can no longer sign you in
                here. Your local password keeps working. You will be asked for it next.
              </p>
              <div class="dialog-actions">
                <button type="button" class="danger" (click)="confirmUnlink()">Unlink Authentik</button>
                <button type="button" class="quiet" (click)="cancelUnlink()">Cancel</button>
              </div>
            </app-dialog>
          }
        } @else {
          <p>
            Not linked. Linking sends you to Authentik to sign in once, then ties that account to
            this owner. Only one account can be linked, and nobody else is added.
          </p>
          <button type="button" [disabled]="busy()" [attr.aria-busy]="busy() ? 'true' : null" (click)="link()">
            Link Authentik
          </button>
        }
      }
    </app-loading>
  `,
})
export class SsoLink implements OnInit {
  private readonly sso = inject(SsoService);

  protected readonly state = signal<SsoState | null>(null);
  protected readonly loading = signal(true);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  ngOnInit(): void {
    this.refresh();
  }

  protected short(subject: string | undefined): string {
    return subject && subject.length > 14 ? `${subject.slice(0, 6)}...${subject.slice(-6)}` : (subject ?? '');
  }

  refresh(): void {
    this.sso.ssoStatus().subscribe({
      next: (state) => {
        this.loading.set(false);
        this.state.set(state);
      },
      error: (err: HttpErrorResponse) => {
        this.loading.set(false);
        this.error.set(linkErrorMessage(err, 'Loading sign-in') ?? 'Could not load sign-in.');
      },
    });
  }

  protected link(): void {
    this.begin();
    this.sso.ssoLink().subscribe({
      next: (intent) => {
        this.busy.set(false);
        this.go(intent.url);
      },
      error: (err: HttpErrorResponse) => this.fail(err, 'Linking'),
    });
  }

  /** Sends the browser to Authentik to finish the link; a seam for tests. */
  protected go(url: string): void {
    window.location.assign(url);
  }

  protected readonly confirmingUnlink = signal(false);

  protected askUnlink(): void {
    this.confirmingUnlink.set(true);
  }

  protected cancelUnlink(): void {
    this.confirmingUnlink.set(false);
  }

  protected confirmUnlink(): void {
    this.confirmingUnlink.set(false);
    this.unlink();
  }

  protected unlink(): void {
    this.begin();
    this.sso.ssoUnlink().subscribe({
      next: (state) => {
        this.busy.set(false);
        this.state.set(state);
      },
      error: (err: HttpErrorResponse) => this.fail(err, 'Unlinking'),
    });
  }

  private begin(): void {
    this.busy.set(true);
    this.error.set(null);
  }

  private fail(err: HttpErrorResponse, action: string): void {
    this.busy.set(false);
    this.error.set(linkErrorMessage(err, action));
  }
}
