import { Component, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';
import { HttpErrorResponse } from '@angular/common/http';
import { SessionService } from '../auth/session.service';

/** Login page (S033): username plus password, session on success. */
@Component({
  imports: [RouterLink],
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
          err.status === 401
            ? 'Invalid username or password.'
            : 'Login failed. Is the daemon reachable?',
        );
      },
    });
  }
}
