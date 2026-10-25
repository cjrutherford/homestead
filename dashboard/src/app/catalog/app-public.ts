import { HttpErrorResponse } from '@angular/common/http';
import { Component, DestroyRef, inject, input, OnInit, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { ApiService, AppPublicPlan, AppPublicRequest, AppPublicStatus, OperationState } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { isTerminal } from '../core/operation-view';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';
import { SessionService } from '../auth/session.service';
import { ApprovedPlan, approvedPlan, Permit } from '../ui/permit';

/** A refusal (400, 422) or a stale plan (409) reads as the appliance's own words. */
export function publicError(failure: HttpErrorResponse): string {
  const reason = typeof failure.error === 'string' ? failure.error.trim() : '';
  const overrides = reason ? { 400: reason, 409: reason, 422: reason } : {};
  return httpErrorMessage(failure, 'Public address', overrides);
}

/**
 * One app's public address on the tunnel (S290, ADR-041 §1), the dashboard
 * side of `homesteadctl apps public`: the names it has, and plan then
 * approve to add or remove one. Adding warns that the app's own sign-in is
 * what protects it.
 */
@Component({
  selector: 'app-app-public',
  imports: [Loading, Permit, StateMessage, OperationProgress, RouterLink],
  template: `
    <app-loading [loading]="!status() && !error()" shape="lines">
      @if (error() && !status()) {
        <app-state-message kind="error" [headline]="error()" actionLabel="Try again" (action)="load()" />
      } @else if (status(); as s) {
        @if (!s.available) {
          <p>A public address is not available: {{ s.reason }}</p>
          @if (s.reason?.includes('Tunnel page')) {
            <p><a routerLink="/tunnel">Open the Tunnel page</a></p>
          }
        } @else {
          @if (publicNames(s).length > 0) {
            <ul aria-label="Public addresses">
              @for (n of publicNames(s); track n.name) {
                <li>
                  <a class="mono" [href]="'https://' + n.name" target="_blank" rel="noopener">{{ n.name }}</a>
                  through the tunnel {{ s.tunnel }}
                  <button type="button" class="quiet" (click)="review({ enabled: false, hostname: n.name })" [disabled]="busy()">
                    Remove
                  </button>
                </li>
              }
            </ul>
          } @else {
            <p>{{ title() }} is reachable only on your network.</p>
          }
          <p>
            <label>
              Public name
              <input
                name="public-hostname"
                autocomplete="off"
                [placeholder]="s.proposed ?? ''"
                [value]="hostname()"
                (input)="hostname.set($any($event.target).value)"
                [disabled]="busy()"
              />
            </label>
            <button type="button" (click)="review({ enabled: true, hostname: hostname().trim() || undefined })" [disabled]="busy()">
              Review public address
            </button>
          </p>
        }
        @if (error()) {
          <app-state-message kind="error" [headline]="error()" />
        }

        @if (plan(); as p) {
          <app-permit
            [heading]="(p.action === 'add' ? 'Publish ' : 'Remove ') + p.hostname"
            [digest]="p.digest"
            [refusal]="refused()"
            [label]="'Public address plan for ' + title()"
          >
            <p>
              @if (p.action === 'add') {
                Publish <span class="mono">{{ p.hostname }}</span> to {{ p.route }} through the tunnel {{ p.tunnel }}.
              } @else {
                Remove <span class="mono">{{ p.hostname }}</span> from the tunnel {{ p.tunnel }}.
              }
            </p>
            <ul aria-label="DNS changes">
              @for (change of p.dns; track change.name + change.action + change.content) {
                <li>
                  {{ change.action }}: {{ change.type }} <span class="mono">{{ change.name }}</span> to <span class="mono">{{ change.content }}</span>
                  @for (old of change.replaces; track old.content) {
                    <br />deletes {{ old.type }} {{ old.content }}
                  }
                </li>
              }
            </ul>
            <dl>
              <dt>Tunnel names before</dt><dd>{{ p.connector_before.join(', ') || 'none' }}</dd>
              <dt>Tunnel names after</dt><dd>{{ p.connector_after.join(', ') || 'none' }}</dd>
            </dl>
            @for (warning of p.warnings; track warning) {
              <p class="hint">{{ warning }}</p>
            }
            @if (p.sign_in) {
              <p class="hint">{{ p.sign_in }}</p>
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
        @if (note()) {
          <app-state-message kind="status" [headline]="note()" />
        }
      }
    </app-loading>
  `,
})
export class AppPublic implements OnInit {
  private readonly api = inject(ApiService);
  private readonly destroyRef = inject(DestroyRef);

  readonly name = input.required<string>();
  readonly title = input.required<string>();

  protected readonly status = signal<AppPublicStatus | null>(null);
  protected readonly plan = signal<AppPublicPlan | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly note = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly hostname = signal('');
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
    this.api.appPublic(this.name()).subscribe({
      next: (status) => this.status.set(status),
      error: (failure: HttpErrorResponse) => this.error.set(publicError(failure)),
    });
  }

  protected publicNames(s: AppPublicStatus): { name: string; route: string }[] {
    return s.routes.flatMap((r) => r.public.map((name) => ({ name, route: r.route })));
  }

  protected review(request: AppPublicRequest): void {
    this.busy.set(true);
    this.error.set(null);
    this.note.set(null);
    this.approved.set(null);
    this.refused.set(null);
    this.api.appPublicPlan(this.name(), request).subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.plan.set(plan);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.plan.set(null);
        this.error.set(publicError(failure));
      },
    });
  }

  protected approve(): void {
    const plan = this.plan();
    if (!plan) {
      return;
    }
    // The plan names what it reviewed, so approval sends exactly that.
    const request: AppPublicRequest =
      plan.action === 'add'
        ? { enabled: true, hostname: plan.hostname, route: plan.route }
        : { enabled: false, hostname: plan.hostname };
    this.busy.set(true);
    this.error.set(null);
    this.api.appPublicApply(this.name(), request, plan.digest).subscribe({
      next: ({ id }) => {
        this.approved.set(
          approvedPlan(
            (plan.action === 'add' ? 'Publish ' : 'Remove ') + plan.hostname,
            plan.digest,
            plan.action === 'add'
              ? `Publish ${plan.hostname} to ${plan.route} through the tunnel ${plan.tunnel}.`
              : `Remove ${plan.hostname} from the tunnel ${plan.tunnel}.`,
            this.sessions.username(),
          ),
        );
        this.plan.set(null);
        this.hostname.set('');
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
                this.note.set(
                  plan.action === 'add'
                    ? `${this.title()} is public at https://${plan.hostname}.`
                    : `${plan.hostname} no longer reaches ${this.title()}.`,
                );
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
          this.error.set(publicError(failure));
          return;
        }
        // The plan stays on screen: the refusal goes on its permit (S309).
        this.refused.set(publicError(failure));
      },
    });
  }
}
