import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService } from '../core/api.service';

type WizardStep = 'token' | 'admin' | 'done';

/**
 * Appliance claim wizard (S032): redeem the console token, then create the
 * first administrator. Already-claimed appliances skip ahead; an existing
 * administrator lands on done. Login itself arrives with S033.
 */
@Component({
  imports: [RouterLink],
  selector: 'app-claim',
  templateUrl: './claim.html',
})
export class Claim implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly step = signal<WizardStep>('token');
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly alreadySetup = signal(false);

  ngOnInit(): void {
    this.api.identity().subscribe({
      next: (identity) => {
        if (identity.state === 'CLAIMED') {
          this.step.set('admin');
        }
      },
      error: () => this.unreachable(),
    });
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
        this.step.set('done');
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        if (err.status === 409) {
          this.alreadySetup.set(true);
          this.step.set('done');
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

  private unreachable(): void {
    this.error.set('Daemon unreachable. Is homestead-server running?');
  }
}
