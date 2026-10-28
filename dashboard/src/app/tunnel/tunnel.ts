import { plural } from '../core/plural';
import { Component, computed, effect, ElementRef, inject, OnInit, signal, input, untracked, viewChild } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, TunnelCertificate, TunnelPlan, TunnelRequest, TunnelStatus } from '../core/api.service';
import { isTerminal } from '../core/operation-view';
import { WorkOrders } from '../core/work-orders.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';
import { SessionService } from '../auth/session.service';
import { ApprovedPlan, approvedPlan, Permit } from '../ui/permit';
import { IssuerMoves } from '../ui/issuer-moves';
import { coverageLamp, coverageRule } from '../core/coverage';

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
 * service address. Since S315 (ADR-044) Authentik answers under the
 * dashboard's name at /apps/auth/, so only an older install is asked for
 * an Authentik name; the page states the shared-origin trade-off once.
 */
@Component({
  imports: [OperationProgress, Loading, StateMessage, Dialog, RouterLink, Permit, IssuerMoves],
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
  /**
   * Whether Authentik still needs a public name of its own: only an
   * install from before S315 (ADR-044) serves it on a name; otherwise it
   * answers at the dashboard's name under /apps/auth/.
   */
  protected readonly authHostnameAsked = computed(() => {
    const state = this.status();
    return state === null || !!state.proposed?.auth_hostname;
  });
  private readonly authInput = viewChild<ElementRef<HTMLInputElement>>('authInput');
  /** The Authentik name typed, when the page asks for one. */
  protected authValue(): string {
    return this.authInput()?.nativeElement.value ?? '';
  }
  protected readonly authHint = computed(
    () => this.status()?.proposed?.auth_hostname || `auth.${this.domain() || 'example.com'}`,
  );
  protected readonly plural = plural;
  protected readonly coverageRule = coverageRule;
  protected readonly lamp = coverageLamp;
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
  /** The plan approved here, kept as its stamped permit (S309). */
  protected readonly approved = signal<ApprovedPlan | null>(null);
  /** S325 (ADR-048 §5): the certificate's terms and production confirmations. */
  protected readonly agreeTerms = signal(false);
  protected readonly confirmProduction = signal(false);
  /** Approving waits for the confirmations the plan's certificate needs. */
  protected readonly canApprove = computed(() => {
    const c = this.plan()?.certificate;
    if (this.busy()) return false;
    if (!c?.issue) return true;
    return this.agreeTerms() && (!this.isProduction(c) || this.confirmProduction());
  });
  /** Why the appliance would not approve the plan on screen. */
  protected readonly refused = signal<string | null>(null);
  private readonly sessions = inject(SessionService);
  /** The masthead sentence: the recorded tunnel and what it serves, from the status the page reads. */
  protected readonly fact = computed(() => {
    const state = this.status();
    if (!state) {
      return 'Reach your apps from outside without changing your router.';
    }
    if (!state.tunnels.length) {
      return 'No tunnel is set up yet. Nothing on this appliance is public through one.';
    }
    const names = state.routes.length === 1 ? '1 public name' : `${state.routes.length} public names`;
    const first = state.tunnels[0];
    const more = state.tunnels.length > 1 ? ` and ${state.tunnels.length - 1} more` : '';
    return `The tunnel ${first.name}${more} serves ${names}. The connector is ${state.connector.active ? 'active' : 'not active'}.`;
  });
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
        this.refused.set(failure.error ?? failure.message);
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
    this.approved.set(null);
    this.refused.set(null);
    this.agreeTerms.set(false);
    this.confirmProduction.set(false);
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
    this.refused.set(null);
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
    this.refused.set(null);
    const approvals = plan.certificate?.issue
      ? { agree_terms: this.agreeTerms(), confirm_production: this.confirmProduction() }
      : {};
    this.api.tunnelSetup(request, plan.digest, approvals).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.plan.set(null);
        this.approved.set(
          approvedPlan(
            (plan.adopt ? 'Adopt the tunnel ' : 'Set up the tunnel ') + plan.name,
            plan.digest,
            `Tunnel ${plan.name} in zone ${plan.zone}, ${plural(plan.ingress.length, 'public name')}.`,
            this.sessions.username(),
          ),
        );
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

  protected isProduction(c: TunnelCertificate): boolean {
    return c.directory_class === 'production';
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
