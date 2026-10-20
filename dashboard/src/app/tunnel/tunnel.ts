import { plural } from '../core/plural';
import { Component, inject, OnInit, signal, input } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ApiService, TunnelStatus } from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';

/**
 * Tunnel remote access page (S247): discovery with tunnel-first
 * guidance, tunnel setup, recorded routes with health, and teardown.
 * The API token seals server-side on setup and never reads back: the
 * form sends it once, status never shows it. This page never changes
 * anything except through setup and teardown.
 */
@Component({
  imports: [OperationProgress, Loading, StateMessage, Dialog],
  selector: 'app-tunnel',
  templateUrl: './tunnel.html',
})
export class Tunnel implements OnInit {
  /** Shown inside the setup wizard (S291): the page's own heading is left to the step. */
  readonly embedded = input(false);
  protected readonly plural = plural;
  private readonly api = inject(ApiService);

  protected readonly status = signal<TunnelStatus | null>(null);
  protected readonly operationId = signal<number | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  /** Inline messages for the setup form, keyed by field name. */
  protected readonly fieldErrors = signal<Record<string, string>>({});
  /** The teardown waiting for confirmation. */
  protected readonly confirming = signal<{ id: string; name: string; accountId: string } | null>(null);
  /** Message under the Account ID of the tunnel being torn down. */
  protected readonly teardownError = signal<{ id: string; message: string } | null>(null);

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.tunnelStatus().subscribe({
      next: (status) => {
        this.busy.set(false);
        this.status.set(status);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  setup(
    name: string,
    hostname: string,
    service: string,
    apiToken: string,
    accountId: string,
  ): void {
    const missing: Record<string, string> = {};
    if (!name) missing['name'] = 'Enter a name for the tunnel.';
    if (!hostname) missing['hostname'] = 'Enter the hostname the tunnel will serve.';
    if (!service) missing['service'] = 'Enter the local address the tunnel forwards to, such as http://127.0.0.1:8080.';
    if (!apiToken) missing['token'] = 'Enter the API token. Homestead seals it on this appliance.';
    if (!accountId) missing['account'] = 'Enter your account ID.';
    this.fieldErrors.set(missing);
    if (Object.keys(missing).length > 0) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.tunnelSetup(name, hostname, service, apiToken, accountId).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.operationId.set(id ?? null);
        this.notice.set('Setup started; the token is now sealed server-side.');
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  /** Asks first: tearing a tunnel down cuts every hostname it serves. */
  askTeardown(id: string, name: string, accountId: string): void {
    if (!accountId) {
      this.teardownError.set({ id, message: 'Enter the account ID. Homestead never guesses which account a tunnel belongs to.' });
      return;
    }
    this.teardownError.set(null);
    this.confirming.set({ id, name, accountId });
  }

  cancelTeardown(): void {
    this.confirming.set(null);
  }

  confirmTeardown(): void {
    const pending = this.confirming();
    if (!pending) return;
    this.confirming.set(null);
    this.teardown(pending.id, pending.accountId);
  }

  teardown(id: string, accountId: string): void {
    if (!accountId) {
      this.teardownError.set({ id, message: 'Enter the account ID. Homestead never guesses which account a tunnel belongs to.' });
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.tunnelTeardown(id, accountId).subscribe({
      next: ({ id: operation }) => {
        this.busy.set(false);
        this.operationId.set(operation ?? null);
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }
}
