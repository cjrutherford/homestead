import { plural } from '../core/plural';
import { Component, computed, inject, OnInit, signal, input } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ApiService, TunnelPlan, TunnelStatus } from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';

/**
 * Tunnel remote access page (S247, planned in S294 per ADR-037):
 * discovery with tunnel-first guidance, a tunnel plan the owner reviews
 * and approves, recorded routes with health, and teardown. The tunnel
 * uses the Cloudflare token from the Domain step and finds the account
 * from the hostname's zone, so this page never asks for either. It never
 * changes anything except through an approved plan and teardown.
 */
@Component({
  imports: [OperationProgress, Loading, StateMessage, Dialog],
  selector: 'app-tunnel',
  templateUrl: './tunnel.html',
})
export class Tunnel implements OnInit {
  /** Shown inside the setup wizard (S291): the page's own heading is left to the step. */
  readonly embedded = input(false);
  /** The setup domain (S294): the hostname is suggested under it. */
  readonly domain = input('');
  protected readonly hostnameHint = computed(() => (this.domain() ? `photos.${this.domain()}` : 'photos.example.com'));
  protected readonly plural = plural;
  private readonly api = inject(ApiService);

  protected readonly status = signal<TunnelStatus | null>(null);
  protected readonly operationId = signal<number | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  /** Inline messages for the setup form, keyed by field name. */
  protected readonly fieldErrors = signal<Record<string, string>>({});
  /** The plan under review; approving sends its digest. */
  protected readonly plan = signal<TunnelPlan | null>(null);
  /** The teardown waiting for confirmation. */
  protected readonly confirming = signal<{ id: string; name: string } | null>(null);

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

  /** Asks the daemon what setup would change. Nothing changes yet. */
  review(name: string, hostname: string, service: string): void {
    const missing: Record<string, string> = {};
    if (!name) missing['name'] = 'Enter a name for the tunnel.';
    if (!hostname) missing['hostname'] = 'Enter the public hostname the tunnel will serve, such as photos.example.com.';
    if (!service) missing['service'] = 'Enter the local address of the app the tunnel forwards to, such as http://127.0.0.1:2283.';
    this.fieldErrors.set(missing);
    if (Object.keys(missing).length > 0) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.plan.set(null);
    this.api.tunnelPlan(name, hostname, service).subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.plan.set(plan);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  /** Back to the form without changing anything. */
  discardPlan(): void {
    this.plan.set(null);
  }

  /** Approves exactly the reviewed plan. */
  approve(): void {
    const plan = this.plan();
    if (!plan) return;
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.tunnelSetup(plan.name, plan.hostname, plan.service, plan.digest).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.plan.set(null);
        this.operationId.set(id ?? null);
        this.notice.set('Setup started: the tunnel, its DNS record and the connector are being created.');
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        if (failure.status === 409) {
          this.plan.set(null);
          this.error.set('Something changed in Cloudflare since you reviewed the plan. Review it again.');
          return;
        }
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  /** Asks first: tearing a tunnel down cuts every hostname it serves. */
  askTeardown(id: string, name: string): void {
    this.confirming.set({ id, name });
  }

  cancelTeardown(): void {
    this.confirming.set(null);
  }

  confirmTeardown(): void {
    const pending = this.confirming();
    if (!pending) return;
    this.confirming.set(null);
    this.teardown(pending.id);
  }

  teardown(id: string): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.tunnelTeardown(id).subscribe({
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
