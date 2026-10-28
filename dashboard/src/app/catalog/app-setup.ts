import { HttpErrorResponse } from '@angular/common/http';
import { Component, DestroyRef, inject, input, OnInit, output, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { Subscription } from 'rxjs';
import { ApiService, AppSetup as AppSetupView, AppSetupStep, AppSetupStepName, OperationState } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { isTerminal } from '../core/operation-view';
import { StateMessage } from '../state/state-message';
import { OperationProgress } from '../ui/operation-progress';
import { Permit } from '../ui/permit';

/** What each setup step does, as the owner reads it. */
export const SETUP_STEP_LABELS: Record<AppSetupStepName, string> = {
  address: 'Address on your network',
  publish: 'Public address',
  'sign-in': 'Sign-in with Authentik',
  people: 'People who can sign in',
};

const STATE_LABELS: Record<AppSetupStep['state'], string> = {
  waiting: 'Waiting',
  running: 'Running',
  done: 'Done',
  changed: 'Plan changed',
  failed: 'Failed',
  skipped: 'Skipped',
};

const STATE_BADGES: Record<AppSetupStep['state'], string> = {
  waiting: 'badge',
  running: 'badge badge-pending',
  done: 'badge badge-ok',
  changed: 'badge badge-warn',
  failed: 'badge badge-fail',
  skipped: 'badge',
};

function setupError(failure: HttpErrorResponse, action: string): string {
  const reason = typeof failure.error === 'string' ? failure.error.trim() : '';
  const overrides = reason ? { 400: reason, 409: reason, 422: reason } : {};
  return httpErrorMessage(failure, action, overrides);
}

/**
 * The setup the owner approved with an install (S324, ADR-048): it runs on
 * the appliance, so this panel only follows it. A stopped setup shows the
 * stopped step's plan as it stands now, to approve again (resume) or to
 * end the setup (skip). The same panel serves the wizard's last step and
 * the catalog card.
 */
@Component({
  selector: 'app-app-setup',
  imports: [FormsModule, Permit, StateMessage, OperationProgress],
  template: `
    @if (setup(); as s) {
      @if (s.status !== 'none') {
        <section class="app-setup" [attr.aria-labelledby]="'app-setup-heading-' + name()">
          <h3 [id]="'app-setup-heading-' + name()">{{ heading(s) }}</h3>
          @if (s.status === 'waiting') {
            <p class="hint">The setup starts on the appliance once the install finishes.</p>
          } @else if (s.status === 'running') {
            <p class="hint">
              The appliance runs these steps in order. You can close this page; the setup keeps going.
            </p>
          }
          <ol class="setup-steps" [attr.aria-label]="'Setup steps for ' + title()">
            @for (step of s.steps; track step.name) {
              <li>
                {{ label(step.name) }}
                <span [class]="badge(step)">{{ stateLabel(step) }}</span>
                @if (step.reason) {
                  <span class="hint"> {{ step.reason }}</span>
                }
              </li>
            }
          </ol>
          @if (s.status === 'stopped') {
            @if (s.reason) {
              <app-state-message kind="error" [headline]="s.reason" />
            }
            @if (!reviewing()) {
              <p>
                <button type="button" (click)="review()" [disabled]="busy()">Review</button>
                <button type="button" class="quiet" (click)="skip()" [disabled]="busy()">Skip the rest</button>
              </p>
            }
          }
        </section>
      }
      @if (reviewing() && s.status === 'stopped' && s.review; as r) {
        @if (r.problem) {
          <app-state-message kind="error" [headline]="r.problem" detail="This step cannot be planned now, so the setup can only be skipped." />
          <p>
            <button type="button" (click)="skip()" [disabled]="busy()">Skip the rest</button>
          </p>
        } @else {
          <app-permit [heading]="label(r.step) + ' for ' + title()" [digest]="r.digest ?? ''" [refusal]="refused()" [label]="'Current plan for ' + title()">
            @if (r.step === 'address') {
              @if (r.addresses?.length) {
                <p>Adds {{ r.addresses!.join(', ') }} to the appliance's addresses.</p>
              } @else {
                <p>{{ title() }} already has its address; nothing changes.</p>
              }
            }
            @if (r.public; as p) {
              <p>Publishes {{ title() }} at <span class="mono">{{ p.url || p.hostname }}</span>.</p>
              @for (setting of p.app_settings ?? []; track setting) {
                <p class="hint">{{ setting }}</p>
              }
              @for (warning of p.warnings; track warning) {
                <p class="hint">{{ warning }}</p>
              }
            }
            @if (r.sign_in; as p) {
              <ul [attr.aria-label]="'Sign-in changes for ' + title()">
                @for (change of p.changes; track change) {
                  <li>{{ change }}</li>
                }
              </ul>
              @for (warning of p.warnings; track warning) {
                <p class="hint">{{ warning }}</p>
              }
              @if (r.token_kept === false) {
                <label>
                  Authentik API token
                  <input type="password" autocomplete="off" [(ngModel)]="token" name="setup-token" />
                </label>
                <p class="hint">Used for this run only; Homestead does not keep it.</p>
              }
            }
            @if (r.people?.length) {
              <p>Lets {{ r.people!.join(', ') }} sign in to {{ title() }}.</p>
            }
            <p class="permit-actions">
              <button type="button" class="primary" (click)="resume()" [disabled]="busy() || (r.step === 'sign-in' && r.token_kept === false && !token)">
                Approve and resume
              </button>
              <button type="button" class="quiet" (click)="skip()" [disabled]="busy()">Skip the rest</button>
              <button type="button" class="quiet" (click)="reviewing.set(false); refused.set(null)" [disabled]="busy()">Cancel</button>
            </p>
          </app-permit>
        }
      }
    }
    @if (operationId(); as id) {
      <app-operation-progress [id]="id" [operation]="operationState()" />
    }
    @if (error()) {
      <app-state-message kind="error" [headline]="error()" />
    }
  `,
})
export class AppSetup implements OnInit {
  private readonly api = inject(ApiService);
  private readonly destroyRef = inject(DestroyRef);

  readonly name = input.required<string>();
  readonly title = input.required<string>();
  /** Emitted each time the setup is read and not running: done, stopped, skipped or none. */
  readonly settled = output<AppSetupView>();

  protected readonly setup = signal<AppSetupView | null>(null);
  protected readonly reviewing = signal(false);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly refused = signal<string | null>(null);
  protected readonly operationId = signal<number | null>(null);
  protected readonly operationState = signal<OperationState | null>(null);
  protected token = '';
  private following: Subscription | null = null;

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.api.appSetup(this.name()).subscribe({
      next: (setup) => {
        this.setup.set(setup);
        if (setup.status === 'running' && setup.operation) {
          this.follow(setup.operation);
          return;
        }
        this.busy.set(false);
        if (setup.status !== 'waiting') {
          this.settled.emit(setup);
        }
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(setupError(failure, 'Reading the setup'));
      },
    });
  }

  protected heading(s: AppSetupView): string {
    switch (s.status) {
      case 'running':
      case 'waiting':
        return `Setting up ${this.title()}`;
      case 'stopped':
        return `${this.title()}'s setup stopped`;
      case 'skipped':
        return `${this.title()}'s setup was skipped`;
      default:
        return `${this.title()} is set up`;
    }
  }

  protected label(step: AppSetupStepName): string {
    return SETUP_STEP_LABELS[step] ?? step;
  }

  protected stateLabel(step: AppSetupStep): string {
    return STATE_LABELS[step.state] ?? step.state;
  }

  protected badge(step: AppSetupStep): string {
    return STATE_BADGES[step.state] ?? 'badge';
  }

  /** Re-reads the setup so the stopped step's plan is current, then shows it. */
  protected review(): void {
    this.reviewing.set(true);
    this.refused.set(null);
    this.load();
  }

  protected resume(): void {
    const review = this.setup()?.review;
    if (!review?.digest) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.refused.set(null);
    const token = this.token;
    this.token = '';
    this.api.appSetupResume(this.name(), review.digest, token).subscribe({
      next: ({ id }) => {
        this.reviewing.set(false);
        this.follow(id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        if (failure.status === 409) {
          // The plan moved again: show the new one to review.
          this.refused.set(setupError(failure, 'Resuming the setup'));
          this.load();
          return;
        }
        this.refused.set(setupError(failure, 'Resuming the setup'));
      },
    });
  }

  protected skip(): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.appSetupSkip(this.name()).subscribe({
      next: (setup) => {
        this.busy.set(false);
        this.reviewing.set(false);
        this.setup.set(setup);
        this.settled.emit(setup);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(setupError(failure, 'Skipping the setup'));
      },
    });
  }

  private follow(id: number): void {
    if (this.operationId() === id && this.following) {
      return;
    }
    this.following?.unsubscribe();
    this.busy.set(true);
    this.operationId.set(id);
    this.operationState.set(null);
    this.following = this.api
      .operationEvents(id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (state) => {
          this.operationState.set(state);
          if (isTerminal(state.status)) {
            this.following?.unsubscribe();
            this.following = null;
            this.load();
          }
        },
        error: () => {
          this.following = null;
          this.busy.set(false);
          this.error.set('Lost the live connection to the setup. It keeps running on the appliance; reload to see it.');
        },
      });
  }
}
