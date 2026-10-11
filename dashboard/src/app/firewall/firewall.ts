import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { forkJoin } from 'rxjs';
import { ApiService, FirewallPlan, FirewallStatus } from '../core/api.service';
import { WizardService } from '../wizard/wizard.service';
import { WizardShell } from '../wizard/wizard-shell';
import { StateMessage } from '../state/state-message';

const WIZARD_ID = 'firewall';

const STEPS = ['Subnets', 'Rules', 'Review', 'Done'];

const STEP_IDS = ['subnets', 'rules', 'review', 'done'];

/**
 * Host firewall setup wizard (S056): LAN identification from live
 * discovery, recommended rules as the rendered script, review of the
 * planned diff, then guarded apply with verification. Progress persists
 * through the wizard framework; the server guard plus snapshot-backed
 * apply protect the management path.
 */
@Component({
  imports: [StateMessage, RouterLink, WizardShell],
  selector: 'app-firewall',
  templateUrl: './firewall.html',
})
export class Firewall implements OnInit {
  private readonly api = inject(ApiService);
  private readonly wizards = inject(WizardService);

  protected readonly steps = STEPS;
  protected readonly stepIndex = signal(0);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly alreadyApplied = signal(false);
  protected readonly status = signal<FirewallStatus | null>(null);
  protected readonly plan = signal<FirewallPlan | null>(null);

  ngOnInit(): void {
    this.busy.set(true);
    forkJoin({
      status: this.api.firewallStatus(),
      plan: this.api.firewallPlan(),
    }).subscribe({
      next: ({ status, plan }) => {
        this.busy.set(false);
        this.status.set(status);
        this.plan.set(plan);
        if (plan.in_sync) {
          this.alreadyApplied.set(true);
          this.stepIndex.set(3);
          return;
        }
        this.wizards.load(WIZARD_ID).subscribe({
          next: (saved) => {
            const index = STEP_IDS.indexOf(saved.step);
            this.stepIndex.set(index >= 0 && index < 3 ? index : 0);
          },
          error: () => undefined,
        });
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(
          err.status === 503
            ? 'Firewall management needs privilege: run the daemon as root with nftables present.'
            : 'Firewall unavailable. Is the daemon reachable?',
        );
      },
    });
  }

  protected next(): void {
    const next = Math.min(this.stepIndex() + 1, 2);
    this.persist(STEP_IDS[next], () => this.stepIndex.set(next));
  }

  protected back(): void {
    this.stepIndex.update((index) => Math.max(index - 1, 0));
  }

  protected confirm(): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.firewallApply().subscribe({
      next: () => {
        this.busy.set(false);
        this.persist('done', () => this.stepIndex.set(3));
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        const serverMessage =
          typeof err.error === 'string' && err.error ? err.error : null;
        this.error.set(
          serverMessage ??
            (err.status === 409
              ? 'Apply refused: the plan would cut the management path.'
              : 'Apply failed. The previous rules were restored.'),
        );
      },
    });
  }

  private persist(step: string, done: () => void): void {
    this.wizards.save(WIZARD_ID, step).subscribe({ next: done, error: done });
  }
}
