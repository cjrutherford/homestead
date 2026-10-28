import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, CraftFilesPlan, CraftFilesStatus } from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { SessionService } from '../auth/session.service';
import { ApprovedPlan, approvedPlan, Permit } from '../ui/permit';
import { Loading } from '../ui/loading';

/**
 * Craft files (S254, ADR-029): link the craft apps to the owner's
 * Nextcloud through the daemon's WebDAV proxy. Link, rotate and unlink
 * are planned and then approved; the app password is typed here, sent
 * once with the approval, and cleared from the component immediately. It
 * is never shown, stored in the browser, or returned by the daemon.
 */
@Component({
  imports: [Loading, Permit, StateMessage, RouterLink],
  selector: 'app-craft-files',
  templateUrl: './craft-files.html',
})
export class CraftFiles implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly status = signal<CraftFilesStatus | null>(null);
  protected readonly plan = signal<CraftFilesPlan | null>(null);
  /** The plan approved here, kept as its stamped permit (S309). */
  protected readonly approved = signal<ApprovedPlan | null>(null);
  /** Why the appliance would not approve the plan on screen. */
  protected readonly refused = signal<string | null>(null);
  private readonly sessions = inject(SessionService);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly loading = signal(true);

  protected baseUrl = '';
  protected account = '';
  protected root = 'Homestead';
  protected createPassword = false;
  protected allowInsecure = false;
  protected appPassword = '';
  protected accountPassword = '';

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.api.craftFilesStatus().subscribe({
      next: (status) => {
        this.loading.set(false);
        this.status.set(status);
      },
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
  }

  /** Review a link, rotate or unlink before anything changes. */
  review(action: 'link' | 'rotate' | 'unlink'): void {
    this.begin();
    this.api
      .craftFilesPlan({
        action,
        base_url: action === 'link' ? this.baseUrl : undefined,
        account: action === 'link' ? this.account : undefined,
        root: action === 'unlink' ? undefined : this.root,
        create_app_password: action === 'unlink' ? undefined : this.createPassword,
        allow_insecure_http: action === 'unlink' || !this.allowInsecure ? undefined : true,
      })
      .subscribe({
        next: (plan) => {
          this.busy.set(false);
          this.approved.set(null);
          this.refused.set(null);
          this.plan.set(plan);
        },
        error: (failure: HttpErrorResponse) => this.fail(failure),
      });
  }

  /**
   * S326: review linking this appliance's own Nextcloud. Homestead creates the
   * app password with the administrator password it generated at the install,
   * so nothing is typed. New craft installs make this link themselves.
   */
  reviewAppliance(): void {
    this.begin();
    this.api.craftFilesPlan({ action: 'link', appliance: true, root: this.root }).subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.approved.set(null);
        this.refused.set(null);
        this.plan.set(plan);
      },
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
  }

  approve(): void {
    const plan = this.plan();
    if (!plan) {
      return;
    }
    this.begin();
    const body = plan.appliance
      ? { plan_id: plan.id }
      : {
          plan_id: plan.id,
          app_password: this.createPassword ? undefined : this.appPassword || undefined,
          account_password: this.createPassword ? this.accountPassword || undefined : undefined,
        };
    // The secrets leave the component before the reply arrives.
    this.appPassword = '';
    this.accountPassword = '';
    this.api.craftFilesApprove(body).subscribe({
      next: (status) => {
        this.busy.set(false);
        this.approved.set(approvedPlan(this.planHeading(plan.action), '', plan.summary.join(' '), this.sessions.username()));
        this.plan.set(null);
        this.status.set(status);
        this.notice.set(
          plan.action === 'unlink'
            ? 'Nextcloud unlinked. Craft apps keep documents in the browser.'
            : 'Nextcloud linked. The app password is sealed on the appliance.',
        );
      },
      error: (failure: HttpErrorResponse) => {
        this.fail(failure);
        // The plan stays on screen: the refusal goes on its permit (S309).
        this.refused.set(this.error());
        this.error.set(null);
      },
    });
  }

  /** The permit's title for a reviewed action. */
  protected planHeading(action: CraftFilesPlan['action']): string {
    return action === 'link' ? 'Link Nextcloud' : action === 'rotate' ? 'Rotate the app password' : 'Unlink Nextcloud';
  }

  cancel(): void {
    this.plan.set(null);
    this.refused.set(null);
    this.appPassword = '';
    this.accountPassword = '';
  }

  toggleLocalOnly(app: string, localOnly: boolean): void {
    this.begin();
    this.api.craftFilesLocalOnly(app, localOnly).subscribe({
      next: (status) => {
        this.busy.set(false);
        this.status.set(status);
      },
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
  }

  /** Opens the craft host's wrapper page with a one-time launch code in the URL fragment. */
  launch(app: string): void {
    this.begin();
    this.api.craftFilesLaunch(app).subscribe({
      next: (result) => {
        this.busy.set(false);
        window.open(result.url, '_blank', 'noopener');
      },
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
  }

  private begin(): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
  }

  private fail(failure: HttpErrorResponse): void {
    this.busy.set(false);
    this.loading.set(false);
    const text = typeof failure.error === 'string' ? failure.error.trim() : '';
    this.error.set(text || `Request failed (${failure.status}).`);
  }
}
