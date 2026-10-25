import { plural } from '../core/plural';
import { Component, computed, effect, inject, OnInit, signal, input, untracked } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, TunnelPlan, TunnelRequest, TunnelStatus } from '../core/api.service';
import { isTerminal } from '../core/operation-view';
import { WorkOrders } from '../core/work-orders.service';
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
 *
 * The plan publishes the dashboard and Authentik through the edge (S296):
 * the owner names them or takes the proposal, and never types a local
 * service address.
 */
@Component({
  imports: [OperationProgress, Loading, StateMessage, Dialog, RouterLink],
  selector: 'app-tunnel',
  templateUrl: './tunnel.html',
})
export class Tunnel implements OnInit {
  /** Shown inside the setup wizard (S291): the page's own heading is left to the step. */
  readonly embedded = input(false);
  /** The setup domain (S294): the names are suggested under it. */
  readonly domain = input('');
  /** The daemon's proposal first, then names under the setup domain. */
  protected readonly dashboardHint = computed(
    () => this.status()?.proposed?.dashboard_hostname || `homestead.${this.domain() || 'example.com'}`,
  );
  protected readonly authHint = computed(
    () => this.status()?.proposed?.auth_hostname || `auth.${this.domain() || 'example.com'}`,
  );
  protected readonly plural = plural;
  private readonly api = inject(ApiService);
  private readonly orders = inject(WorkOrders);

  protected readonly status = signal<TunnelStatus | null>(null);
  protected readonly operationId = signal<number | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  /** Why the last Review plan failed; it sits beside the button, not at the top. */
  protected readonly reviewError = signal<string | null>(null);

  /** Inline messages for the setup form, keyed by field name. */
  protected readonly fieldErrors = signal<Record<string, string>>({});
  /** The plan under review; approving sends its digest. */
  protected readonly plan = signal<TunnelPlan | null>(null);
  /** The request the plan answered; approving sends it again. */
  private reviewed: TunnelRequest | null = null;
  /** The teardown waiting for confirmation. */
  protected readonly confirming = signal<{ id: string; name: string } | null>(null);

  constructor() {
    // When setup or teardown ends, read the tunnels again.
    let refreshedFor: number | null = null;
    effect(() => {
      const id = this.operationId();
      const op = id === null ? null : this.orders.stateOf(id);
      if (op && isTerminal(op.status) && refreshedFor !== id) {
        refreshedFor = id;
        untracked(() => {
          if (op.status === 'succeeded') this.notice.set(null);
          this.refresh();
        });
      }
    });
  }

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
  review(name: string, dashboardHostname = '', authHostname = '', adopt = ''): void {
    const missing: Record<string, string> = {};
    if (!name.trim() && !adopt.trim()) missing['name'] = 'Enter a name for the tunnel, or the ID of one to adopt.';
    if (name.trim() && adopt.trim()) missing['name'] = 'Either name a new tunnel or adopt one, not both.';
    this.fieldErrors.set(missing);
    if (Object.keys(missing).length > 0) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.reviewError.set(null);
    this.notice.set(null);
    this.plan.set(null);
    // Adopting (S290) takes over a tunnel this appliance's connector already serves.
    const request: TunnelRequest = adopt.trim() ? { adopt: adopt.trim().toLowerCase() } : { name: name.trim() };
    if (dashboardHostname.trim()) request.dashboard_hostname = dashboardHostname.trim();
    if (authHostname.trim()) request.auth_hostname = authHostname.trim();
    this.reviewed = request;
    this.api.tunnelPlan(request).subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.plan.set(plan);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        // Shown under Review plan, where the owner is looking (S303).
        this.reviewError.set(failure.error ?? failure.message);
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
    const request = this.reviewed;
    if (!plan || !request) return;
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.tunnelSetup(request, plan.digest).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.plan.set(null);
        this.operationId.set(id ?? null);
        this.notice.set('Setup started: the tunnel, its DNS records, the edge names and the connector are being created.');
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
