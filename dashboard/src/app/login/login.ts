import { Component, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';
import { HttpErrorResponse } from '@angular/common/http';
import { SessionService } from '../auth/session.service';
import { httpErrorMessage } from '../core/http-error';
import { StateMessage } from '../state/state-message';

/** Login page (S033): username plus password, session on success. */
@Component({
  imports: [StateMessage, RouterLink],
  selector: 'app-login',
  templateUrl: './login.html',
})
export class Login {
  private readonly sessions = inject(SessionService);
  private readonly router = inject(Router);

  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly sessionsState = this.sessions;

  protected submit(username: string, password: string): void {
    if (!username.trim() || !password) {
      this.error.set('Username and password are required.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.sessions.login(username.trim(), password).subscribe({
      next: () => {
        this.busy.set(false);
        void this.router.navigateByUrl('/status');
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(
          httpErrorMessage(err, 'Login', {
            401: 'Invalid username or password.',
            429: 'This account is temporarily locked after repeated failed attempts. Wait a while and try again.',
          }),
        );
      },
    });
  }
}
