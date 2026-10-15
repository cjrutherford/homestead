import { Component, computed, inject, OnDestroy, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { Subscription } from 'rxjs';
import { RouterLink } from '@angular/router';
import {
  ApiService,
  OperationState,
  TlsIssuancePlan,
  TlsAccountTerms,
  TlsRenewalStatus,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';

const TERMINAL = new Set(['succeeded', 'failed', 'cancelled']);

/**
 * Certificates (S278): owner-approved first issuance, matching
 * `homesteadctl tls issuance plan|approve|apply`. The plan is a pure
 * preview (no certificate authority is contacted). Approving binds to the
 * exact plan digest shown, records agreement to the CA's terms, and for a
 * production CA needs a separate explicit confirmation. Applying runs the
 * issuance as an operation; progress streams here. Private keys are never
 * shown: the plan names where they are placed, nothing more.
 */
@Component({
  imports: [StateMessage, RouterLink],
  selector: 'app-tls',
  templateUrl: './tls.html',
  styles: [
    'button, input { min-height: 44px; }',
    '.tls-plan { overflow-wrap: anywhere; }',
  ],
})
export class Tls implements OnInit, OnDestroy {
  private readonly api = inject(ApiService);
  private stream: Subscription | null = null;

  protected readonly plans = signal<TlsIssuancePlan[]>([]);
  /** Renewal status per certificate (S272): failures and owner actions. */
  protected readonly renewals = signal<TlsRenewalStatus[]>([]);
  protected readonly loading = signal(true);
  protected readonly loadError = signal<string | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  /** Per-certificate consent, never preselected. */
  protected readonly agreed = signal<Record<string, boolean>>({});
  protected readonly production = signal<Record<string, boolean>>({});
  /** Opt-out of unattended renewal, never preselected (auto is the default). */
  protected readonly manualRenewal = signal<Record<string, boolean>>({});
  /** Current CA terms per certificate whose account needs re-registering. */
  protected readonly terms = signal<Record<string, TlsAccountTerms>>({});
  protected readonly termsAgreed = signal<Record<string, boolean>>({});
  protected readonly operationId = signal<number | null>(null);
  protected readonly operation = signal<OperationState | null>(null);
  protected readonly streamError = signal<string | null>(null);

  protected readonly running = computed(() => {
    const op = this.operation();
    return this.operationId() !== null && !this.streamError() && !(op && TERMINAL.has(op.status));
  });
  protected readonly locked = computed(() => this.busy() || this.running());

  ngOnInit(): void {
    this.load();
  }

  ngOnDestroy(): void {
    this.stream?.unsubscribe();
  }

  load(): void {
    this.api.tlsRenewal().subscribe({
      next: (report) => {
        this.renewals.set(report.certificates ?? []);
        for (const status of report.certificates ?? []) {
          if (status.account_recovery_required) {
            this.api.tlsAccountTerms(status.certificate).subscribe({
              next: (t) => this.terms.update((m) => ({ ...m, [status.certificate]: t })),
              error: () => undefined,
            });
          }
        }
      },
      error: () => this.renewals.set([]),
    });
    this.loading.set(true);
    this.loadError.set(null);
    this.api.tlsIssuancePlans().subscribe({
      next: ({ plans }) => {
        this.loading.set(false);
        this.plans.set(plans ?? []);
      },
      error: (failure: HttpErrorResponse) => {
        this.loading.set(false);
        this.plans.set([]);
        this.loadError.set(
          failure.status === 404
            ? 'No TLS strategy is saved yet. Save one in the TLS setup wizard, then come back to review issuance.'
            : this.describe(failure),
        );
      },
    });
  }

  protected describe(failure: HttpErrorResponse): string {
    if (failure.status === 0) {
      return 'Cannot reach the Homestead daemon. Check that it is running and try again.';
    }
    const body = failure.error;
    if (typeof body === 'string' && body.trim()) {
      return body.trim();
    }
    return failure.message;
  }

  setAgreed(certificate: string, value: boolean): void {
    this.agreed.update((m) => ({ ...m, [certificate]: value }));
  }

  setManualRenewal(certificate: string, value: boolean): void {
    this.manualRenewal.update((m) => ({ ...m, [certificate]: value }));
  }

  setTermsAgreed(certificate: string, value: boolean): void {
    this.termsAgreed.update((m) => ({ ...m, [certificate]: value }));
  }

  /** Production re-registration needs the fresh terms agreement. */
  protected canReregister(certificate: string): boolean {
    if (this.locked()) {
      return false;
    }
    const t = this.terms()[certificate];
    return !(t?.agreement_required ?? true) || !!this.termsAgreed()[certificate];
  }

  setProduction(certificate: string, value: boolean): void {
    this.production.update((m) => ({ ...m, [certificate]: value }));
  }

  /**
   * Stores the DNS provider credentials typed into the plan's form. The
   * values go straight from the form to the daemon: they are never kept
   * in component state, and the form is cleared once sent.
   */
  saveCredentials(plan: TlsIssuancePlan, form: HTMLFormElement): void {
    if (this.locked()) {
      return;
    }
    const data = new FormData(form);
    const values: Record<string, string> = {};
    for (const field of plan.credentials) {
      values[field.name] = String(data.get(field.name) ?? '');
    }
    if (Object.values(values).some((value) => value === '')) {
      this.error.set('Fill in every credential field.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.tlsIssuanceCredentials(plan.provider, values).subscribe({
      next: () => {
        form.reset();
        this.busy.set(false);
        this.notice.set(`Stored the ${plan.provider} credentials. They are write-only: they cannot be read back.`);
        this.load();
      },
      error: (failure: HttpErrorResponse) => {
        form.reset();
        this.busy.set(false);
        this.error.set(this.describe(failure));
      },
    });
  }

  /** Approval needs the terms; production CAs also need their own box. */
  protected canApprove(plan: TlsIssuancePlan): boolean {
    if (!plan.ready || this.locked()) {
      return false;
    }
    if (!this.agreed()[plan.certificate]) {
      return false;
    }
    return plan.directory_class !== 'production' || !!this.production()[plan.certificate];
  }

  approve(plan: TlsIssuancePlan): void {
    if (!this.canApprove(plan)) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api
      .tlsIssuanceApprove({
        certificate: plan.certificate,
        digest: plan.digest,
        agree_terms: true,
        confirm_production: !!this.production()[plan.certificate],
        renewal: this.manualRenewal()[plan.certificate] ? 'manual' : 'auto',
      })
      .subscribe({
        next: () => {
          this.busy.set(false);
          this.notice.set(`Approved the plan for ${plan.certificate}. Nothing has been issued yet.`);
          this.load();
        },
        error: (failure: HttpErrorResponse) => {
          this.busy.set(false);
          this.error.set(this.describe(failure));
          this.load();
        },
      });
  }

  /** The CA forgot the ACME account: the owner registers a new one. */
  reregisterAccount(certificate: string): void {
    if (!this.canReregister(certificate)) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.tlsReregisterAccount(certificate, !!this.termsAgreed()[certificate]).subscribe({
      next: () => {
        this.busy.set(false);
        this.notice.set(`Registered a new ACME account for ${certificate}. Unattended renewal may resume.`);
        this.load();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(this.describe(failure));
        this.load();
      },
    });
  }

  apply(plan: TlsIssuancePlan): void {
    if (!plan.approved || this.locked()) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.operation.set(null);
    this.streamError.set(null);
    this.api.tlsIssuanceApply(plan.certificate).subscribe({
      next: (started) => {
        this.busy.set(false);
        this.notice.set(`Issuance started for ${plan.certificate}.`);
        this.follow(started.id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(this.describe(failure));
        this.load();
      },
    });
  }

  private follow(id: number): void {
    this.stream?.unsubscribe();
    this.operationId.set(id);
    this.stream = this.api.operationEvents(id).subscribe({
      next: (state) => {
        this.operation.set(state);
        if (TERMINAL.has(state.status)) {
          this.load();
        }
      },
      error: () =>
        this.streamError.set(
          'Lost the live progress stream. Open the operation to see its state.',
        ),
    });
  }
}
