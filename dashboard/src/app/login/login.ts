import { Component, OnInit, inject, signal } from '@angular/core';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { HttpErrorResponse } from '@angular/common/http';
import { SessionService } from '../auth/session.service';
import { ApiService } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { ssoErrorMessage } from '../core/sso-errors';
import { StateMessage } from '../state/state-message';

/**
 * Login page (S033): username plus password, session on success. When
 * sign-in with Authentik is set up and linked (S286, ADR-033) it also offers
 * that, beside the form. The offer comes from the daemon's local state and
 * Authentik is never asked, so a dead Authentik cannot hide or slow this page.
 */
@Component({
  imports: [StateMessage, RouterLink],
  selector: 'app-login',
  templateUrl: './login.html',
})
export class Login implements OnInit {
  private readonly sessions = inject(SessionService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  private readonly api = inject(ApiService);

  /** The daemon's browser-protocol address that starts the Authentik flow. */
  protected readonly ssoLogin = '/api/v1/sso/login';
  protected readonly ssoEnabled = signal(false);

  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly sessionsState = this.sessions;

  ngOnInit(): void {
    const query = this.route.snapshot.queryParamMap;
    const failure = ssoErrorMessage(query.get('sso_error'));
    if (failure) {
      this.error.set(failure);
    }
    if (query.get('sso') === 'done') {
      this.finishSso();
      return;
    }
    if (!this.sessions.isLoggedIn()) {
      // 404 (not set up, or not linked) simply leaves the button out.
      this.api.ssoOptions().subscribe({
        next: (options) => this.ssoEnabled.set(options.enabled),
        error: () => this.ssoEnabled.set(false),
      });
    }
  }

  /** The callback set the session cookies; learn who it is and carry on. */
  private finishSso(): void {
    this.api.lockoutStatus().subscribe({
      next: (status) => {
        this.sessions.begin(status.username, '');
        void this.router.navigateByUrl('/status');
      },
      error: () =>
        this.error.set('Signed in with Authentik, but the dashboard could not confirm the session. Try again, or use your local password.'),
    });
  }

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
