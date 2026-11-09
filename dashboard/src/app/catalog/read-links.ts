import { HttpErrorResponse } from '@angular/common/http';
import { Component, DestroyRef, inject, input, OnInit, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ApiService, OperationState, ReadLinksPlan, ReadLinksStatus } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { isTerminal } from '../core/operation-view';
import { StateMessage } from '../state/state-message';
import { OperationProgress } from '../ui/operation-progress';
import { SessionService } from '../auth/session.service';
import { ApprovedPlan, approvedPlan, Permit } from '../ui/permit';

function linksError(failure: HttpErrorResponse): string {
  const reason = typeof failure.error === 'string' ? failure.error.trim() : '';
  const overrides = reason ? { 400: reason, 409: reason, 422: reason } : {};
  return httpErrorMessage(failure, 'Public read links', overrides);
}

/**
 * Public read and view links for an Everything app (S433, ADR-058), the
 * dashboard side of `homesteadctl apps read-links`. Only the two link
 * paths ever open on the public name; nothing changes until the owner
 * approves the reviewed plan. Shown only for apps that have links.
 */
@Component({
  selector: 'app-read-links',
  imports: [Permit, StateMessage, OperationProgress],
  template: `
    @if (status(); as s) {
      <section class="read-links" aria-labelledby="read-links-heading">
        <h3 id="read-links-heading">Public read links</h3>
        @if (!s.available) {
          <p>{{ s.reason }}</p>
        } @else if (s.exposed) {
          <p>
            Read and view links for {{ title() }} open on <span class="mono">{{ s.origin }}</span> ({{ s.links }} live).
            Nothing else of {{ title() }} is reachable there.
          </p>
        } @else {
          <p>
            Links minted in {{ title() }} work on this network only. You can open just their two link paths on the
            public name. Nothing changes until you approve the plan.
          </p>
        }
        @if (s.available && !plan() && !approved()) {
          <p>
            <button type="button" (click)="review(!s.exposed)" [disabled]="busy()">
              {{ s.exposed ? 'Review closing public links' : 'Review opening public links' }}
            </button>
          </p>
        }
      </section>
    }
    @if (error()) {
      <app-state-message kind="error" [headline]="error()" />
    }
    @if (plan(); as p) {
      <app-permit [heading]="(p.action === 'expose' ? 'Open' : 'Close') + ' public read links for ' + title()" [digest]="p.digest" [refusal]="refused()" [label]="'Public read links plan for ' + title()">
        <p>Public name <span class="mono">{{ p.origin }}</span> through <span class="mono">{{ p.route }}</span>.</p>
        <ul aria-label="Public paths">
          @for (x of p.patterns; track x) {
            <li class="mono">{{ x }}</li>
          }
        </ul>
        <ul aria-label="Stays closed">
          @for (x of p.refused; track x) {
            <li>Stays closed: {{ x }}</li>
          }
        </ul>
        <p class="hint">{{ p.links }} live links. Links expire within {{ p.expiry_cap_days }} days.</p>
        @for (warning of p.warnings; track warning) {
          <p class="hint">{{ warning }}</p>
        }
        <p class="permit-actions">
          <button type="button" class="primary" (click)="approve()" [disabled]="busy()">Approve</button>
          <button type="button" class="quiet" (click)="plan.set(null); refused.set(null)" [disabled]="busy()">Cancel</button>
        </p>
      </app-permit>
    }
    @if (approved(); as a) {
      <app-permit [heading]="a.heading" [digest]="a.digest" [approval]="a.approval">
        <p>{{ a.summary }}</p>
      </app-permit>
    }
    @if (operationId(); as id) {
      <app-operation-progress [id]="id" [operation]="operationState()" />
    }
  `,
})
export class ReadLinks implements OnInit {
  private readonly api = inject(ApiService);
  private readonly destroyRef = inject(DestroyRef);
  private readonly sessions = inject(SessionService);

  readonly name = input.required<string>();
  readonly title = input.required<string>();

  protected readonly status = signal<ReadLinksStatus | null>(null);
  protected readonly plan = signal<ReadLinksPlan | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly operationId = signal<number | null>(null);
  protected readonly operationState = signal<OperationState | null>(null);
  protected readonly approved = signal<ApprovedPlan | null>(null);
  protected readonly refused = signal<string | null>(null);

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    // Apps without links answer 404: the panel simply is not shown.
    this.api.readLinks(this.name()).subscribe({
      next: (status) => this.status.set(status),
      error: () => this.status.set(null),
    });
  }

  protected review(enabled: boolean): void {
    this.busy.set(true);
    this.error.set(null);
    this.refused.set(null);
    this.api.readLinksPlan(this.name(), enabled).subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.plan.set(plan);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(linksError(failure));
      },
    });
  }

  protected approve(): void {
    const plan = this.plan();
    if (!plan) {
      return;
    }
    const enabled = plan.action === 'expose';
    this.busy.set(true);
    this.error.set(null);
    this.api.readLinksApprove(this.name(), enabled, plan.digest).subscribe({
      next: ({ id }) => {
        this.approved.set(
          approvedPlan(
            `${enabled ? 'Open' : 'Close'} public read links for ${this.title()}`,
            plan.digest,
            `${enabled ? 'Open' : 'Close'} ${plan.patterns.join(' and ')} on ${plan.origin}.`,
            this.sessions.username(),
          ),
        );
        this.plan.set(null);
        this.operationId.set(id);
        this.operationState.set(null);
        this.api
          .operationEvents(id)
          .pipe(takeUntilDestroyed(this.destroyRef))
          .subscribe({
            next: (state) => {
              this.operationState.set(state);
              if (!isTerminal(state.status)) {
                return;
              }
              this.busy.set(false);
              if (state.status !== 'succeeded') {
                this.error.set(state.error || 'The change did not finish. Open the operation to see why.');
              }
              this.load();
            },
            error: () => {
              this.busy.set(false);
              this.error.set('Lost the live connection to the operation. Open it to see how it ended.');
            },
          });
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        if (failure.status === 409) {
          this.plan.set(null);
          this.error.set(linksError(failure));
          return;
        }
        this.refused.set(linksError(failure));
      },
    });
  }
}
