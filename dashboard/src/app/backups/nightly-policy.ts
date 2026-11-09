import { HttpErrorResponse } from '@angular/common/http';
import { Component, inject, OnInit, output, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import {
  ApiService,
  BackupPolicyPlan,
  BackupPolicyRequest,
  BackupPolicyStatus,
} from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { StateMessage } from '../state/state-message';
import { SessionService } from '../auth/session.service';
import { ApprovedPlan, approvedPlan, Permit } from '../ui/permit';
import { Loading } from '../ui/loading';

/** A refusal (422) or a stale plan (409) reads as the appliance's own words. */
function policyError(failure: HttpErrorResponse): string {
  const reason = typeof failure.error === 'string' ? failure.error.trim() : '';
  const overrides = reason ? { 409: reason, 422: reason } : {};
  return httpErrorMessage(failure, 'Nightly backup policy', overrides);
}

/**
 * The nightly backup policy (S297, ADR-039 §2), shared by the Backups page
 * and the setup step: the proposed or approved policy, the apps it covers
 * and leaves out, and plan then approve. Nothing changes until the owner
 * approves the reviewed plan; the same request with its digest applies it.
 */
@Component({
  imports: [Loading, Permit, StateMessage, RouterLink],
  selector: 'app-nightly-policy',
  template: `
    <app-loading [loading]="!status() && !error()" shape="lines">
      @if (error() && !status()) {
        <app-state-message kind="error" [headline]="error()" actionLabel="Try again" (action)="load()" />
      } @else if (status(); as s) {
        @if (s.policy; as p) {
          <p class="policy-current">
            Approved policy:
            @if (p.enabled) {
              <strong>on</strong>, every night at {{ p.time_of_day }} ({{ s.time_zone }}), keeping the newest
              {{ p.retain }} runs of each app.
            } @else {
              <strong>off</strong>.
            }
          </p>
        } @else {
          <p class="policy-current">
            Not approved yet. Proposed: every night at {{ s.proposed.time_of_day }} ({{ s.time_zone }}), keeping the
            newest {{ s.proposed.retain }} runs of each app.
          </p>
        }
        @if (!s.backup_storage) {
          <app-state-message
            kind="notice"
            headline="Nightly backups need a disk for the backup storage class."
            detail="Assign the backup class on the Storage page, then come back. You can still turn the policy off without one."
            actionLabel="Go to Storage"
            actionLink="/admin/storage"
          />
        }
        <p>
          @if (s.covered.length === 0) {
            Covers no app yet: no app that keeps data is installed. Apps installed later are covered.
          } @else {
            Covers: {{ s.covered.join(', ') }}. Apps installed later are covered too.
          }
        </p>
        @if (s.skipped.length > 0) {
          <ul aria-label="Apps the policy leaves out">
            @for (skip of s.skipped; track skip.package) {
              <li><strong>{{ skip.package }}</strong>: {{ skip.reason }}</li>
            }
          </ul>
        }

        <form (submit)="review(); $event.preventDefault()" aria-label="Nightly backup policy">
          <label>
            <input type="checkbox" name="policy-enabled" [checked]="enabled()" (change)="edit('enabled', $any($event.target).checked)" [disabled]="busy()" />
            Back up every night
          </label>
          <label>
            Time of day ({{ s.time_zone }})
            <input type="time" name="policy-time" [value]="timeOfDay()" (input)="edit('time_of_day', $any($event.target).value)" [disabled]="busy() || !enabled()" />
          </label>
          <label>
            Runs to keep per app
            <input type="number" name="policy-retain" min="1" max="366" [value]="retain()" (input)="edit('retain', +$any($event.target).value)" [disabled]="busy() || !enabled()" />
          </label>
          <button type="submit" [disabled]="busy()">Review the policy</button>
        </form>
        @if (error()) {
          <app-state-message kind="error" [headline]="error()" />
        }

        @if (plan(); as p) {
          <app-permit heading="Nightly backup policy" [digest]="p.digest" [refusal]="refused()" label="Policy plan">
            <h4>Will change</h4>
            <ul aria-label="What approving changes">
              @for (change of p.changes; track change) {
                <li>{{ change }}</li>
              }
            </ul>
            <!-- The plan's own coverage, which its first line refers to (S305). -->
            <ul aria-label="Apps the plan backs up">
              @for (app of p.covered; track app) {
                <li>{{ app }}</li>
              } @empty {
                <li>No app that keeps data is installed yet.</li>
              }
            </ul>
            @if (p.skipped.length > 0) {
              <ul aria-label="Apps the plan leaves out">
                @for (skip of p.skipped; track skip.package) {
                  <li><strong>{{ skip.package }}</strong>: {{ skip.reason }}</li>
                }
              </ul>
            }
            <p class="hint">Nothing changes until you approve. Each run is listed under Operations and in the audit log.</p>
            <p class="permit-actions">
              <button type="button" class="primary" (click)="approve()" [disabled]="busy()">Approve the policy</button>
              <button type="button" class="quiet" (click)="plan.set(null); refused.set(null)" [disabled]="busy()">Cancel</button>
            </p>
          </app-permit>
        }
        @if (permit(); as a) {
          <app-permit [heading]="a.heading" [digest]="a.digest" [approval]="a.approval">
            <p>{{ a.summary }}</p>
          </app-permit>
        }
        @if (approvedNote()) {
          <app-state-message kind="status" [headline]="approvedNote()" />
          @if (!s.backup_storage) {
            <p><a routerLink="/admin/storage">Assign backup storage</a></p>
          }
        }
      }
    </app-loading>
  `,
})
export class NightlyPolicy implements OnInit {
  private readonly api = inject(ApiService);

  /** Emits the new status after an approval. */
  readonly approved = output<BackupPolicyStatus>();

  protected readonly status = signal<BackupPolicyStatus | null>(null);
  protected readonly plan = signal<BackupPolicyPlan | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly approvedNote = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly timeOfDay = signal('02:00');
  protected readonly retain = signal(7);
  protected readonly enabled = signal(true);
  /** The request the shown plan was made for; approval sends exactly this. */
  private reviewed: BackupPolicyRequest | null = null;
  /** The policy approved here, kept as its stamped permit (S309). */
  protected readonly permit = signal<ApprovedPlan | null>(null);
  /** Why the appliance would not approve the plan on screen. */
  protected readonly refused = signal<string | null>(null);
  private readonly sessions = inject(SessionService);

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.error.set(null);
    this.api.backupPolicy().subscribe({
      next: (status) => this.show(status),
      error: (failure: HttpErrorResponse) => this.error.set(policyError(failure)),
    });
  }

  protected edit(field: keyof BackupPolicyRequest, value: string | number | boolean): void {
    if (field === 'enabled') {
      this.enabled.set(value as boolean);
    } else if (field === 'time_of_day') {
      this.timeOfDay.set(value as string);
    } else {
      this.retain.set(value as number);
    }
    this.plan.set(null);
  }

  protected review(): void {
    const request: BackupPolicyRequest = {
      time_of_day: this.timeOfDay(),
      retain: this.retain(),
      enabled: this.enabled(),
    };
    this.busy.set(true);
    this.error.set(null);
    this.approvedNote.set(null);
    this.permit.set(null);
    this.refused.set(null);
    this.api.backupPolicyPlan(request).subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.reviewed = request;
        this.plan.set(plan);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.plan.set(null);
        this.error.set(policyError(failure));
      },
    });
  }

  protected approve(): void {
    const plan = this.plan();
    if (!plan || !this.reviewed) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.backupPolicyApply(this.reviewed, plan.digest).subscribe({
      next: (status) => {
        this.busy.set(false);
        this.permit.set(approvedPlan('Nightly backup policy', plan.digest, plan.changes.join(' '), this.sessions.username()));
        this.plan.set(null);
        this.reviewed = null;
        this.approvedNote.set(
          status.policy?.enabled ? 'Nightly backups are on.' : 'Nightly backups are off.',
        );
        this.show(status);
        this.approved.emit(status);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        if (failure.status === 409) {
          this.plan.set(null);
          this.error.set(policyError(failure));
          return;
        }
        // The plan stays on screen: the refusal goes on its permit (S309).
        this.refused.set(policyError(failure));
      },
    });
  }

  private show(status: BackupPolicyStatus): void {
    this.status.set(status);
    const policy = status.policy ?? status.proposed;
    this.timeOfDay.set(policy.time_of_day);
    this.retain.set(policy.retain);
    this.enabled.set(status.policy ? policy.enabled : true);
  }
}
