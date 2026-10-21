import { Component, DestroyRef, OnInit, effect, inject, signal, untracked, input } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ActivatedRoute, Router } from '@angular/router';
import { httpErrorMessage } from '../core/http-error';
import { isTerminal } from '../core/operation-view';
import { ssoErrorMessage } from '../core/sso-errors';
import { WorkOrders } from '../core/work-orders.service';
import { SetupService } from '../setup/setup.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';
import { SsoLink } from './sso-link';
import { SsoPlan, SsoService, SsoState } from './sso.service';

/**
 * Sign-in settings (S286, ADR-033, under System): status, the plan, review,
 * approve and apply flow that configures Authentik, the link between the
 * owner and an Authentik account, and the switch that turns it all off.
 * The Authentik API token is typed here, sent with the plan (to read what
 * exists) and again with the approval, and cleared from the component as
 * soon as each request leaves. It is never stored by the browser.
 */
@Component({
  imports: [Dialog, Loading, OperationProgress, SsoLink, StateMessage],
  selector: 'app-sign-in',
  templateUrl: './sign-in.html',
})
export class SignIn implements OnInit {
  /** Shown inside the setup wizard (S291): the page's own heading is left to the step. */
  readonly embedded = input(false);
  private readonly sso = inject(SsoService);
  private readonly route = inject(ActivatedRoute);
  private readonly orders = inject(WorkOrders);
  private readonly router = inject(Router);
  private readonly setup = inject(SetupService);

  protected readonly state = signal<SsoState | null>(null);
  protected readonly plan = signal<SsoPlan | null>(null);
  protected readonly operationId = signal<number | null>(null);
  protected readonly loading = signal(true);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly confirmingOff = signal(false);
  /** True while the form for a new or changed plan is open on a configured appliance. */
  protected readonly editing = signal(false);

  protected authentikUrl = '';
  protected originsText = '';
  protected signingKey = '';
  protected apiToken = '';
  protected keepToken = false;

  constructor() {
    // When the apply operation ends, read the state again.
    effect(() => {
      const id = this.operationId();
      const op = id === null ? null : this.orders.stateOf(id);
      if (op && isTerminal(op.status)) {
        untracked(() => this.finishApply(op.status === 'succeeded', op.error));
      }
    });
  }

  ngOnInit(): void {
    const query = this.route.snapshot.queryParamMap;
    if (!this.embedded() && (query.get('linked') || query.get('sso_error'))) {
      this.returnToSetup(query.get('linked'), query.get('sso_error'));
    }
    if (query.get('linked')) {
      this.notice.set('Authentik is linked. The login page now offers Sign in with Authentik.');
    }
    const failure = ssoErrorMessage(query.get('sso_error'));
    if (failure) {
      this.error.set(failure);
    }
    this.refresh();
  }

  /**
   * The link round trip through Authentik always lands here. When it was
   * started from the setup wizard (setup not yet complete), go back to the
   * wizard with the same outcome so the owner keeps their place (S292).
   */
  private returnToSetup(linked: string | null, ssoError: string | null): void {
    this.setup.setupStatus().subscribe({
      next: (status) => {
        if (!status.legacy && !status.complete) {
          void this.router.navigate(['/setup'], {
            queryParams: { linked: linked ?? undefined, sso_error: ssoError ?? undefined },
            replaceUrl: true,
          });
        }
      },
      error: () => undefined,
    });
  }

  protected refresh(): void {
    this.sso.ssoStatus().subscribe({
      next: (state) => {
        this.loading.set(false);
        this.state.set(state);
        this.authentikUrl ||= state.defaults.authentik_url;
        this.originsText ||= (state.defaults.origins ?? []).join('\n');
      },
      error: (err: HttpErrorResponse) => {
        this.loading.set(false);
        this.error.set(httpErrorMessage(err, 'Loading sign-in'));
      },
    });
  }

  protected openForm(): void {
    const state = this.state();
    const spec = state?.spec;
    if (spec) {
      this.authentikUrl = spec.authentik_url;
      // A re-plan (S296) adds the tunnel's public dashboard names.
      const origins = [...spec.origins];
      for (const origin of state?.replan ? (state.defaults.public_origins ?? []) : []) {
        if (!origins.includes(origin)) origins.push(origin);
      }
      this.originsText = origins.join('\n');
      this.signingKey = spec.signing_key;
    }
    this.editing.set(true);
  }

  protected review(): void {
    this.begin();
    const token = this.apiToken.trim();
    this.sso
      .ssoPlan({
        authentik_url: this.authentikUrl.trim() || undefined,
        origins: this.originsText.split('\n').map((line) => line.trim()).filter(Boolean),
        signing_key: this.signingKey.trim() || undefined,
        api_token: token || undefined,
      })
      .subscribe({
        next: (plan) => {
          this.busy.set(false);
          this.plan.set(plan);
        },
        error: (err: HttpErrorResponse) => this.fail(err, 'Reviewing the plan'),
      });
  }

  protected approve(): void {
    const plan = this.plan();
    if (!plan) {
      return;
    }
    this.begin();
    const token = this.apiToken.trim();
    this.apiToken = '';
    this.sso.ssoApprove({ digest: plan.digest, api_token: token || undefined, keep_token: this.keepToken }).subscribe({
      next: (state) => {
        this.busy.set(false);
        this.state.set(state);
        this.plan.set(null);
        this.apply();
      },
      error: (err: HttpErrorResponse) => this.fail(err, 'Approving the plan'),
    });
  }

  protected apply(): void {
    this.begin();
    this.sso.ssoApply().subscribe({
      next: (started) => {
        this.busy.set(false);
        this.operationId.set(started.id);
      },
      error: (err: HttpErrorResponse) => this.fail(err, 'Setting up sign-in'),
    });
  }

  protected cancelReview(): void {
    this.plan.set(null);
    this.apiToken = '';
  }

  protected askTurnOff(): void {
    this.confirmingOff.set(true);
  }

  protected turnOff(): void {
    this.confirmingOff.set(false);
    this.begin();
    this.sso.ssoDisable().subscribe({
      next: (state) => {
        this.busy.set(false);
        this.state.set(state);
        this.editing.set(false);
        this.notice.set('Sign-in with Authentik is off. Your local password is unchanged.');
      },
      error: (err: HttpErrorResponse) => this.fail(err, 'Turning off sign-in'),
    });
  }

  private finishApply(ok: boolean, failure: string): void {
    this.operationId.set(null);
    this.editing.set(false);
    if (ok) {
      this.notice.set('Sign-in with Authentik is set up. Link your account to use it.');
    } else {
      this.error.set(failure ? `Setting up sign-in failed: ${failure}` : 'Setting up sign-in failed.');
    }
    this.refresh();
  }

  private begin(): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
  }

  private fail(err: HttpErrorResponse, action: string): void {
    this.busy.set(false);
    const body = typeof err.error === 'string' ? err.error.trim() : '';
    const code = err.headers?.get('X-Homestead-Error');
    if (err.status === 403 && code?.startsWith('reauth')) {
      this.error.set('Nothing changed. This needs your local password.');
      return;
    }
    this.error.set(
      body && [409, 422, 502].includes(err.status) ? body : httpErrorMessage(err, action),
    );
  }
}
