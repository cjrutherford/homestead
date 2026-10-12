import { Component, inject, OnInit, signal } from '@angular/core';
import { StateMessage } from '../state/state-message';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import {
  ApiService,
  ExposureApproval,
  ExposurePlan,
  TlsRenewalStatus,
} from '../core/api.service';

/**
 * Exposure review page (S247): select a route and target, review the
 * recomputed plan, approve under a named actor, and apply through the
 * operation engine. The selection saves through the generic wizard
 * endpoint; review always recomputes from saved inputs, so the page
 * shows the plan or the reason there is none yet. This page never
 * changes anything except through approve and apply.
 */
@Component({
  imports: [StateMessage, RouterLink],
  selector: 'app-exposure',
  templateUrl: './exposure.html',
})
export class Exposure implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly plan = signal<ExposurePlan | null>(null);
  protected readonly approvals = signal<ExposureApproval[]>([]);
  protected readonly renewals = signal<TlsRenewalStatus[]>([]);
  protected readonly operationId = signal<number | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  ngOnInit(): void {
    this.refresh();
    this.api.tlsRenewal().subscribe({
      next: (renewals) => this.renewals.set(renewals ?? []),
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
        if (failure.status !== 404) {
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
    if (!host || !targetHost || !Number.isInteger(port) || port < 1) {
      this.error.set('Route host, target host, and target port are required.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
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
      this.error.set('Approval needs a named actor.');
      return;
    }
    const networks = networksRaw
      .split(',')
      .map((network) => network.trim())
      .filter((network) => network.length > 0);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.exposureApprove(actor, networks).subscribe({
      next: (approval) => {
        this.busy.set(false);
        this.notice.set(
          `Approved ${approval.host} by ${approval.actor}.`,
        );
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
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
