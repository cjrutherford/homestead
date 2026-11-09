import { Component, computed, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { Router, RouterLink } from '@angular/router';
import { SessionService } from '../auth/session.service';
import { ApiService } from '../core/api.service';
import { WizardService } from '../wizard/wizard.service';
import { WizardShell } from '../wizard/wizard-shell';
import { httpErrorMessage, isUnreachable } from '../core/http-error';
import { SetupService } from '../setup/setup.service';
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
  protected readonly sessions = inject(SessionService);
  private readonly router = inject(Router);
  private readonly setup = inject(SetupService);

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
        if (identity.setup_pending) {
          // Token redeemed but no administrator yet (a reload or failure
          // between the steps): carry on at the account step.
          this.step.set('admin');
          return;
        }
        this.wizards.load(WIZARD_ID).subscribe({
          next: (saved) => {
            if (saved.step === 'done') {
              this.alreadySetup.set(true);
              this.step.set('done');
            } else {
              // Saved progress short of done: the admin step is pending.
              this.step.set('admin');
            }
          },
          error: (err: HttpErrorResponse) => {
            // The admin form is only offered when the wizard says it is
            // pending. A refusal (401/403, claim-window-closed) or a missing
            // save on a claimed appliance means setup is finished.
            if (isUnreachable(err) || err.status >= 500) {
              this.unreachable();
              return;
            }
            this.alreadySetup.set(true);
            this.step.set('done');
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
      this.error.set('Enter the claim token from sudo homesteadctl claim token.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.claim(token).subscribe({
      next: () => {
        this.busy.set(false);
        // Claim completed and no session exists yet; wizard saves need
        // one now. Resume derives from appliance state, so nothing is lost.
        this.step.set('admin');
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        if (err.status === 409) {
          this.step.set('admin');
          return;
        }
        this.error.set(
          err.status === 401
            ? 'Invalid or expired token.'
            : httpErrorMessage(err, 'Claim'),
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
      next: (created) => {
        this.busy.set(false);
        // Setup logs the owner in (the response set the session cookie),
        // so the wizard save below carries a session (ADR-020, S257).
        this.sessions.begin(created.username, created.expires_at ?? '');
        this.persist('done', () => this.confirmAndContinue());
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        if (err.status === 409) {
          this.alreadySetup.set(true);
          this.step.set('done'); // no session here: nothing to save with
          return;
        }
        this.error.set(
          httpErrorMessage(err, 'Setup', {
            403: 'This appliance has not been claimed yet. Enter the claim token first.',
            422: 'Invalid administrator details (usernames are lowercase, passwords at least 12 characters).',
          }),
        );
      },
    });
  }

  /**
   * The owner just chose this password, so setup's "confirm your password"
   * is already answered: record it, then open the setup wizard (S292).
   * Both are best-effort; the done step still links on if either fails.
   */
  private confirmAndContinue(): void {
    this.step.set('done');
    const next = () => void this.router.navigate(['/setup']);
    this.setup.setupStep('account', { state: 'done' }).subscribe({ next, error: next });
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
    this.error.set("Can't reach the appliance. Check that homestead-server is running.");
  }
}
