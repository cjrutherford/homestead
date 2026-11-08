import { HttpErrorResponse } from '@angular/common/http';
import { Component, DestroyRef, inject, input, OnInit, output, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ApiService, AppMovePlan, AppMoveStatus, OperationState } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { isTerminal } from '../core/operation-view';
import { StateMessage } from '../state/state-message';
import { OperationProgress } from '../ui/operation-progress';
import { SessionService } from '../auth/session.service';
import { ApprovedPlan, approvedPlan, Permit } from '../ui/permit';

function moveError(failure: HttpErrorResponse): string {
  const reason = typeof failure.error === 'string' ? failure.error.trim() : '';
  const overrides = reason ? { 400: reason, 409: reason, 422: reason } : {};
  return httpErrorMessage(failure, 'Move', overrides);
}

/**
 * The move offer for an app installed under a name of its own (S315,
 * ADR-044 §5), the dashboard side of `homesteadctl apps move`: shown only
 * when the appliance offers it, and nothing moves until the owner approves
 * the reviewed plan. Sign-in is re-planned by the owner afterwards.
 */
@Component({
  selector: 'app-app-move',
  imports: [Permit, StateMessage, OperationProgress],
  template: `
    @if (status(); as s) {
      @if (s.offered) {
        <section class="app-move" aria-labelledby="app-move-heading">
          <h3 id="app-move-heading">Move to {{ s.to[0] }}</h3>
          <p>
            {{ title() }} answers at its own name, <span class="mono">{{ s.from }}</span>. Apps now live under the
            appliance's name, at <span class="mono">{{ s.path }}</span>. Moving is your choice; nothing changes until
            you approve the plan.
          </p>
          @if (s.public_from; as publicFrom) {
            <p data-move-public>
              {{ title() }} is also public at <span class="mono">{{ publicFrom }}</span>. The move publishes it at
              <span class="mono">{{ s.public_to }}</span> and removes that name. Old bookmarks and links to it stop
              working; nothing redirects.
            </p>
          }
          @if (!plan() && !approved()) {
            <p>
              <button type="button" (click)="review()" [disabled]="busy()">Review the move</button>
            </p>
          }
        </section>
      }
    }
    @if (error()) {
      <app-state-message kind="error" [headline]="error()" />
    }
    @if (plan(); as p) {
      <app-permit [heading]="'Move ' + title() + ' to ' + p.path" [digest]="p.digest" [refusal]="refused()" [label]="'Move plan for ' + title()">
        <p>
          From <span class="mono">{{ p.from }}</span> to
          @for (to of p.to; track to; let last = $last) {
            <span class="mono">{{ to }}</span>{{ last ? '.' : ', ' }}
          }
        </p>
        <dl>
          <dt>Path prefix</dt>
          <dd>{{ p.strip_prefix ? 'Taken off before ' + title() + ' sees the request' : title() + ' serves under it' }}</dd>
          @if (p.redirects.length) {
            <dt>Redirects</dt>
            <dd>{{ p.redirects.join('; ') }}</dd>
          }
        </dl>
        @if (p.public_from) {
          <dl data-move-public>
            <dt>Public address</dt>
            <dd>
              <span class="mono">{{ p.public_from }}</span> is removed. {{ title() }} answers publicly at
              <span class="mono">{{ p.public_to }}</span>.
            </dd>
          </dl>
          @if (p.removes?.length) {
            <ul aria-label="Removed with the old public name">
              @for (removed of p.removes; track removed) {
                <li>{{ removed }}</li>
              }
            </ul>
          }
          @if (p.stops?.length) {
            <p class="hint" data-move-stops>Stops working, with no redirect: <span class="mono">{{ p.stops?.join(', ') }}</span></p>
          }
        }
        @if (p.app_settings.length) {
          <ul aria-label="App settings">
            @for (setting of p.app_settings; track setting) {
              <li class="mono">{{ setting }}</li>
            }
          </ul>
        }
        @for (note of p.sign_in; track note) {
          <p class="hint">{{ note }}</p>
        }
        @for (warning of p.warnings; track warning) {
          <p class="hint">{{ warning }}</p>
        }
        <p class="permit-actions">
          <button type="button" class="primary" (click)="approve()" [disabled]="busy()">Approve the move</button>
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
    @if (note()) {
      <app-state-message kind="status" [headline]="note()" />
    }
  `,
})
export class AppMove implements OnInit {
  private readonly api = inject(ApiService);
  private readonly destroyRef = inject(DestroyRef);
  private readonly sessions = inject(SessionService);

  readonly name = input.required<string>();
  readonly title = input.required<string>();
  /** Emitted once the move finished, so the page can re-read the app's addresses. */
  readonly moved = output<void>();

  protected readonly status = signal<AppMoveStatus | null>(null);
  protected readonly plan = signal<AppMovePlan | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly note = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly operationId = signal<number | null>(null);
  protected readonly operationState = signal<OperationState | null>(null);
  protected readonly approved = signal<ApprovedPlan | null>(null);
  protected readonly refused = signal<string | null>(null);

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    // The offer is a convenience: a failed read leaves it out.
    this.api.appMove(this.name()).subscribe({
      next: (status) => this.status.set(status),
      error: () => this.status.set(null),
    });
  }

  protected review(): void {
    this.busy.set(true);
    this.error.set(null);
    this.refused.set(null);
    this.api.appMovePlan(this.name()).subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.plan.set(plan);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(moveError(failure));
      },
    });
  }

  protected approve(): void {
    const plan = this.plan();
    if (!plan) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.appMoveApply(this.name(), plan.digest).subscribe({
      next: ({ id }) => {
        this.approved.set(
          approvedPlan(
            `Move ${this.title()} to ${plan.path}`,
            plan.digest,
            `Move ${this.title()} from ${plan.from} to ${plan.to.join(', ')}.`,
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
              if (state.status === 'succeeded') {
                this.note.set(`${this.title()} now answers at ${plan.to[0]}. Re-plan its sign-in if it has one.`);
                this.moved.emit();
              } else {
                this.error.set(state.error || 'The move did not finish. Open the operation to see why.');
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
          this.error.set(moveError(failure));
          return;
        }
        this.refused.set(moveError(failure));
      },
    });
  }
}
