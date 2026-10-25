import { HttpErrorResponse } from '@angular/common/http';
import { Component, DestroyRef, inject, input, OnInit, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { ApiService, AppSignInPlan, AppSignInStatus, OperationState } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { isTerminal } from '../core/operation-view';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';
import { SessionService } from '../auth/session.service';
import { ApprovedPlan, approvedPlan, Permit } from '../ui/permit';

/** A refusal (422) or a stale plan (409) reads as the appliance's own words. */
export function signInError(failure: HttpErrorResponse): string {
  const reason = typeof failure.error === 'string' ? failure.error.trim() : '';
  const overrides = reason ? { 409: reason, 422: reason } : {};
  return httpErrorMessage(failure, 'Sign-in with Authentik', overrides);
}

/**
 * Sign-in with Authentik for one app (S298, ADR-039 §3), the dashboard side
 * of `homesteadctl apps sign-in`: whether it is on, a re-plan notice when
 * the app's names changed, and plan then approve to turn it on or off. The
 * Authentik API token is asked for only when the appliance did not keep
 * one; it goes with this approval and is not stored.
 */
@Component({
  selector: 'app-app-sign-in',
  imports: [Loading, Permit, StateMessage, OperationProgress, RouterLink],
  template: `
    <app-loading [loading]="!status() && !error()" shape="lines">
      @if (error() && !status()) {
        <app-state-message kind="error" [headline]="error()" actionLabel="Try again" (action)="load()" />
      } @else if (status(); as s) {
        @if (s.enabled) {
          <p>
            People sign in to {{ title() }} with their Authentik account.
            @if (s.sign_in_url) {
              <a [href]="s.sign_in_url" target="_blank" rel="noopener">Sign in with Authentik</a>.
            }
            The app's own admin account keeps working.
          </p>
          @if (s.replan) {
            <app-state-message
              kind="notice"
              [headline]="title() + '’s names changed since sign-in was set up.'"
              detail="Review the plan to update the addresses Authentik sends people back to."
            />
          }
        } @else if (s.offered) {
          <p>
            {{ title() }} can sign people in with their Authentik account. Its own accounts keep working.
          </p>
        } @else {
          <p>Sign-in with Authentik is not available: {{ s.reason }}</p>
          @if (s.reason?.includes('dashboard sign-in')) {
            <p><a routerLink="/sign-in">Set up dashboard sign-in</a></p>
          }
        }
        @if (s.plugin; as plugin) {
          <p class="hint">
            Uses {{ plugin.name }} {{ plugin.version }}, a third-party Jellyfin plugin Homestead installs
            at this pinned release only.
          </p>
        }
        @if (s.offered || s.enabled) {
          <p>
            @if (s.enabled) {
              <button type="button" (click)="review(true)" [disabled]="busy()">Review sign-in{{ s.replan ? ' changes' : '' }}</button>
              <button type="button" class="quiet" (click)="review(false)" [disabled]="busy()">Turn sign-in off</button>
            } @else {
              <button type="button" (click)="review(true)" [disabled]="busy()">Set up sign-in with Authentik</button>
            }
          </p>
        }
        @if (error()) {
          <app-state-message kind="error" [headline]="error()" />
        }

        @if (plan(); as p) {
          <app-permit
            [heading]="(p.enabled ? 'Turn on sign-in for ' : 'Turn off sign-in for ') + title()"
            [digest]="p.digest"
            [refusal]="refused()"
            [label]="'Sign-in plan for ' + title()"
          >
            <h4>Will change</h4>
            <ul aria-label="What approving changes">
              @for (change of p.changes; track change) {
                <li>{{ change }}</li>
              }
            </ul>
            @if (p.enabled) {
              <dl>
                <dt>Authentik provider</dt><dd>{{ p.provider }}</dd>
                <dt>Authentik application</dt><dd>{{ p.application }}</dd>
                <dt>Client ID</dt><dd class="mono">{{ p.client_id }}</dd>
                <dt>Redirect addresses</dt>
                <dd>
                  <ul>
                    @for (uri of p.redirect_uris; track uri) {
                      <li class="mono">{{ uri }}</li>
                    }
                  </ul>
                </dd>
              </dl>
            }
            @if (p.app_settings.length > 0) {
              <h4>{{ title() }} settings Homestead changes</h4>
              <ul>
                @for (line of p.app_settings; track line) {
                  <li>{{ line }}</li>
                }
              </ul>
            }
            @for (warning of p.warnings; track warning) {
              <p class="hint">{{ warning }}</p>
            }
            @if (!s.token_kept) {
              <label>
                Authentik API token (used for this change only, not kept)
                <input
                  type="password"
                  name="sign-in-token"
                  autocomplete="off"
                  [value]="token()"
                  (input)="token.set($any($event.target).value)"
                  [disabled]="busy()"
                />
              </label>
            }
            <p class="permit-actions">
              <button type="button" class="primary" (click)="approve()" [disabled]="busy() || (!s.token_kept && !token().trim())">
                Approve
              </button>
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
      }
    </app-loading>
  `,
})
export class AppSignIn implements OnInit {
  private readonly api = inject(ApiService);
  private readonly destroyRef = inject(DestroyRef);

  readonly name = input.required<string>();
  readonly title = input.required<string>();

  protected readonly status = signal<AppSignInStatus | null>(null);
  protected readonly plan = signal<AppSignInPlan | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly note = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly token = signal('');
  protected readonly operationId = signal<number | null>(null);
  protected readonly operationState = signal<OperationState | null>(null);
  /** The plan approved here, kept as its stamped permit (S309). */
  protected readonly approved = signal<ApprovedPlan | null>(null);
  /** Why the appliance would not approve the plan on screen. */
  protected readonly refused = signal<string | null>(null);
  private readonly sessions = inject(SessionService);

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.error.set(null);
    this.api.appSignIn(this.name()).subscribe({
      next: (status) => this.status.set(status),
      error: (failure: HttpErrorResponse) => this.error.set(signInError(failure)),
    });
  }

  protected review(enabled: boolean): void {
    this.busy.set(true);
    this.error.set(null);
    this.note.set(null);
    this.approved.set(null);
    this.refused.set(null);
    this.api.appSignInPlan(this.name(), enabled).subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.plan.set(plan);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.plan.set(null);
        this.error.set(signInError(failure));
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
    this.api.appSignInApply(this.name(), plan.enabled, plan.digest, this.token().trim()).subscribe({
      next: ({ id }) => {
        this.approved.set(
          approvedPlan(
            (plan.enabled ? 'Turn on sign-in for ' : 'Turn off sign-in for ') + this.title(),
            plan.digest,
            plan.changes.join(' '),
            this.sessions.username(),
          ),
        );
        this.token.set('');
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
                this.note.set(plan.enabled ? `Sign-in with Authentik is on for ${this.title()}.` : `Sign-in with Authentik is off for ${this.title()}.`);
              } else {
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
          this.error.set(signInError(failure));
          return;
        }
        // The plan stays on screen: the refusal goes on its permit (S309).
        this.refused.set(signInError(failure));
      },
    });
  }
}
