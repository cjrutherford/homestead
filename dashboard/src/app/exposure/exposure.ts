import { Component, inject, OnInit, signal, input } from '@angular/core';
import { SentencePipe } from '../core/format-time';
import { isNotSetUp } from '../core/http-error';
import { StateMessage } from '../state/state-message';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import {
  ApiService,
  ExposureApproval,
  ExposurePlan,
  TlsRenewalReport,
} from '../core/api.service';
import { OperationProgress } from '../ui/operation-progress';
import { Approval, approvedNow, Permit } from '../ui/permit';

/**
 * Exposure review page (S247): select a route and target, review the
 * recomputed plan, approve under a named actor, and apply through the
 * operation engine. The selection saves through the generic wizard
 * endpoint; review always recomputes from saved inputs, so the page
 * shows the plan or the reason there is none yet. This page never
 * changes anything except through approve and apply.
 */
@Component({
  imports: [OperationProgress, Permit, StateMessage, RouterLink, SentencePipe],
  selector: 'app-exposure',
  templateUrl: './exposure.html',
})
export class Exposure implements OnInit {
  /** Shown inside the setup wizard (S291): the page's own heading is left to the step. */
  readonly embedded = input(false);
  private readonly api = inject(ApiService);

  protected readonly plan = signal<ExposurePlan | null>(null);
  protected readonly approvals = signal<ExposureApproval[]>([]);
  protected readonly renewals = signal<TlsRenewalReport | null>(null);
  protected readonly operationId = signal<number | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  /** The approval given here for the plan on screen, stamped on its permit (S309). */
  protected readonly approved = signal<Approval | null>(null);
  /** Why the appliance would not approve the plan on screen. */
  protected readonly refused = signal<string | null>(null);

  /** Inline messages for the selection and approval forms, keyed by field. */
  protected readonly fieldErrors = signal<Record<string, string>>({});

  ngOnInit(): void {
    this.refresh();
    this.api.tlsRenewal().subscribe({
      next: (report) => this.renewals.set(report),
      error: (failure: HttpErrorResponse) =>
        this.error.set(failure.error ?? failure.message),
    });
  }

  refresh(): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.exposureReview().subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.plan.set(plan);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.plan.set(null);
        if (!isNotSetUp(failure)) {
          this.error.set(failure.error ?? failure.message);
        }
      },
    });
    this.api.exposureApprovals().subscribe({
      next: (approvals) => this.approvals.set(approvals ?? []),
      error: (failure: HttpErrorResponse) =>
        this.error.set(failure.error ?? failure.message),
    });
  }

  save(
    host: string,
    targetHost: string,
    targetPort: string,
    to: string,
    zone: string,
    publicIp: string,
  ): void {
    const port = Number(targetPort);
    const missing: Record<string, string> = {};
    if (!host) missing['host'] = 'Enter the route host, such as app.example.com.';
    if (!targetHost) missing['target'] = 'Enter the host the route forwards to.';
    if (!Number.isInteger(port) || port < 1) missing['port'] = 'Enter the port as a whole number, 1 or higher.';
    this.fieldErrors.set(missing);
    if (Object.keys(missing).length > 0) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.approved.set(null);
    this.refused.set(null);
    const payload = {
      route: {
        Host: host,
        Target: { Host: targetHost, Port: port },
        Exposure: 'local',
        TLS: 'internal',
      },
      to,
      zone,
      public_ip: publicIp,
    };
    this.api.saveWizardState('exposure', 'done', payload).subscribe({
      next: () => {
        this.notice.set('Selection saved; review recomputed below.');
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  approve(actor: string, networksRaw: string): void {
    if (!actor) {
      this.fieldErrors.set({ actor: 'Enter your name. Approvals are recorded under it.' });
      return;
    }
    this.fieldErrors.set({});
    const networks = networksRaw
      .split(',')
      .map((network) => network.trim())
      .filter((network) => network.length > 0);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.refused.set(null);
    this.api.exposureApprove(actor, networks).subscribe({
      next: (approval) => {
        this.busy.set(false);
        this.approved.set(approvedNow(approval.actor));
        this.notice.set(
          `Approved ${approval.host} by ${approval.actor}.`,
        );
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        // The plan stays on screen: the refusal goes on its permit.
        this.refused.set(failure.error ?? failure.message);
      },
    });
  }

  apply(host: string): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.exposureApply(host).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.operationId.set(id ?? null);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }
}
