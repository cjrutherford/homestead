import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, NamesStatus, ResolverPlan } from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import { SessionService } from '../auth/session.service';
import { ApprovedPlan, approvedPlan, Permit } from '../ui/permit';

/**
 * App names page (S295, ADR-039): each app's local names, announced as
 * `<app>.local` over mDNS by default, and the optional `.lan` resolver,
 * turned on or off only through a plan the owner approves.
 */
@Component({
  imports: [Loading, StateMessage, RouterLink, Permit],
  selector: 'app-names',
  templateUrl: './names.html',
})
export class Names implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly status = signal<NamesStatus | null>(null);
  protected readonly plan = signal<ResolverPlan | null>(null);
  /** The plan approved here, kept as its stamped permit (S309). */
  protected readonly approved = signal<ApprovedPlan | null>(null);
  /** Why the appliance would not approve the plan on screen. */
  protected readonly refused = signal<string | null>(null);
  private readonly sessions = inject(SessionService);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.busy.set(true);
    this.api.names().subscribe({
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

  /** Asks what turning the resolver on or off changes. Nothing changes yet. */
  review(enabled: boolean): void {
    this.busy.set(true);
    this.error.set(null);
    this.approved.set(null);
    this.refused.set(null);
    this.notice.set(null);
    this.api.resolverPlan(enabled).subscribe({
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

  discardPlan(): void {
    this.plan.set(null);
    this.refused.set(null);
  }

  /** Approves exactly the reviewed plan. */
  approve(): void {
    const plan = this.plan();
    if (!plan) return;
    this.busy.set(true);
    this.error.set(null);
    this.refused.set(null);
    this.api.resolverApply(plan.enabled, plan.digest).subscribe({
      next: (status) => {
        this.busy.set(false);
        this.plan.set(null);
        this.approved.set(
          approvedPlan(
            plan.enabled ? 'Turn on the resolver' : 'Turn off the resolver',
            plan.digest,
            plan.changes.join(' '),
            this.sessions.username(),
          ),
        );
        this.status.set(status);
        if (!status.resolver.enabled) {
          this.notice.set('The resolver is off. Apply the firewall plan to remove its port 53 rules.');
        } else if (status.resolver.running) {
          this.notice.set('The resolver is answering. Apply the firewall plan to add its port 53 rules.');
        }
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        if (failure.status === 409) {
          this.plan.set(null);
          this.error.set('Something changed since you reviewed the plan. Review it again.');
          return;
        }
        this.refused.set(failure.error ?? failure.message);
      },
    });
  }
}
