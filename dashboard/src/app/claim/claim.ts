import { Component, computed, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService } from '../core/api.service';
import { WizardService } from '../wizard/wizard.service';
import { WizardShell } from '../wizard/wizard-shell';
import { StateMessage } from '../state/state-message';

type WizardStep = 'token' | 'admin' | 'done';

const WIZARD_ID = 'claim';

const STEPS = ['Claim token', 'Administrator', 'Done'];

/**
 * Appliance claim wizard (S032), framed by the reusable shell and resumed
 * through persisted progress (S036): redeem the console token, then create
 * the first administrator. Step selection always follows the appliance
 * state first — UNCLAIMED means the token step regardless of saved
 * progress — so stale saves can never misdirect. Only step names persist;
 * tokens and passwords never touch wizard state.
 */
@Component({
  imports: [StateMessage, RouterLink, WizardShell],
  selector: 'app-claim',
  templateUrl: './claim.html',
})
export class Claim implements OnInit {
  private readonly api = inject(ApiService);
  private readonly wizards = inject(WizardService);

  protected readonly step = signal<WizardStep>('token');
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly alreadySetup = signal(false);
  protected readonly steps = STEPS;
  protected readonly stepIndex = computed(() =>
    this.step() === 'token' ? 0 : this.step() === 'admin' ? 1 : 2,
  );

  ngOnInit(): void {
    this.api.identity().subscribe({
      next: (identity) => {
        if (identity.state !== 'CLAIMED') {
          return;
        }
        this.wizards.load(WIZARD_ID).subscribe({
          next: (saved) => {
            if (saved.step === 'done') {
              this.alreadySetup.set(true);
              this.step.set('done');
            } else {
              this.step.set('admin');
            }
          },
          error: () => {
            this.step.set('admin');
          },
        });
      },
      error: () => this.unreachable(),
    });
  }

  protected goBack(): void {
    if (this.step() === 'admin') {
      this.step.set('token');
    } else if (this.step() === 'done') {
      this.step.set('admin');
    }
  }

  protected submitToken(rawToken: string): void {
    const token = rawToken.trim();
    if (!token) {
      this.error.set('Enter the claim token shown on the console.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.claim(token).subscribe({
      next: () => {
        this.busy.set(false);
        this.advanceTo('admin');
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        if (err.status === 409) {
          this.advanceTo('admin');
          return;
        }
        this.error.set(
          err.status === 401
            ? 'Invalid or expired token.'
            : 'Claim failed. Is the daemon reachable?',
        );
      },
    });
  }

  protected submitAdmin(username: string, password: string, confirm: string): void {
    const name = username.trim();
    if (!name || !password) {
      this.error.set('Username and password are required.');
      return;
    }
    if (password !== confirm) {
      this.error.set('Passwords do not match.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.setupAdmin(name, password).subscribe({
      next: () => {
        this.busy.set(false);
        this.advanceTo('done');
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        if (err.status === 409) {
          this.alreadySetup.set(true);
          this.advanceTo('done');
          return;
        }
        this.error.set(
          err.status === 422
            ? 'Invalid administrator details (usernames are lowercase, passwords at least 12 characters).'
            : 'Setup failed. Is the daemon reachable?',
        );
      },
    });
  }

  private advanceTo(step: WizardStep): void {
    this.persist(step, () => this.step.set(step));
  }

  private persist(step: WizardStep, done: () => void): void {
    // Progress tracking is best-effort: the appliance state already drives
    // resume, so a failed save never blocks onboarding.
    this.wizards.save(WIZARD_ID, step).subscribe({
      next: done,
      error: done,
    });
  }

  private unreachable(): void {
    this.error.set('Daemon unreachable. Is homestead-server running?');
  }
}
