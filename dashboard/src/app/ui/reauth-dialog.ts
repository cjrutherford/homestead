import { Component, Injector, OnInit, inject, input, signal } from '@angular/core';
import { ReauthRequest } from '../core/reauth';
import { SsoService } from '../sso/sso.service';
import { Dialog } from './dialog';

/**
 * Asks for the local owner password again (S286, ADR-033). Mounted once in
 * the shell inside a deferred block, so it costs nothing until a link,
 * unlink or step-up action needs it.
 */
@Component({
  selector: 'app-reauth-dialog',
  imports: [Dialog],
  template: `
    @if (request().localOnly) {
      <app-dialog heading="Use your home network" (dismissed)="request().answer(null)">
        <p>
          This action only works from your home network or overlay; open the dashboard at
          @if (localName(); as name) {
            <strong>{{ name }}</strong>
          } @else {
            your local or overlay address
          }
          to continue.
        </p>
        <div class="dialog-actions">
          <button type="button" class="quiet" (click)="request().answer(null)">Close</button>
        </div>
      </app-dialog>
    } @else if (request().stepUp) {
      <app-dialog heading="Sign in with Authentik again" (dismissed)="request().answer(null)">
        <p>
          This action needs a fresh Authentik sign-in, even though you are signed in. Your local
          password is never used on this address. The step-up lasts five minutes.
        </p>
        @if (failed()) {
          <p role="alert">Authentik could not be reached. Try again.</p>
        }
        <div class="dialog-actions">
          <button type="button" [disabled]="going()" (click)="signInAgain()">Sign in with Authentik again</button>
          <button type="button" class="quiet" (click)="request().answer(null)">Cancel</button>
        </div>
      </app-dialog>
    } @else {
      <app-dialog heading="Confirm with your local password" (dismissed)="request().answer(null)">
        <p>
          This needs your local owner password, even though you are signed in. It is sent once for
          this action and not kept.
        </p>
        @if (request().invalid) {
          <p role="alert">That password is not right. Try again.</p>
        }
        <div class="field">
          <label for="reauth-password">Local password</label>
          <input
            id="reauth-password"
            #password
            type="password"
            autocomplete="current-password"
            (keydown.enter)="submit(password.value)"
          />
        </div>
        <div class="dialog-actions">
          <button type="button" (click)="submit(password.value)">Confirm</button>
          <button type="button" class="quiet" (click)="request().answer(null)">Cancel</button>
        </div>
      </app-dialog>
    }
  `,
})
export class ReauthDialog implements OnInit {
  readonly request = input.required<ReauthRequest>();

  private readonly injector = inject(Injector);
  protected readonly going = signal(false);
  protected readonly failed = signal(false);
  /** Where the owner opens the dashboard locally, once known (S335). */
  protected readonly localName = signal<string | null>(null);
  /** Leaves for Authentik; replaceable in tests. */
  protected assign = (url: string): void => window.location.assign(url);

  ngOnInit(): void {
    if (this.request().localOnly) {
      this.findLocalName();
    }
  }

  /** Picks a dashboard address from the sign-in setup that is not this one. */
  private findLocalName(): void {
    const here = window.location.origin;
    this.injector.get(SsoService).ssoStatus().subscribe({
      next: (state) => {
        const publics = state.defaults.public_origins ?? [];
        const all = [...(state.spec?.origins ?? []), ...(state.defaults.origins ?? [])];
        this.localName.set(all.find((o) => o !== here && !publics.includes(o)) ?? null);
      },
      error: () => this.localName.set(null),
    });
  }

  /** Starts the step-up and goes to Authentik, returning to this page. */
  protected signInAgain(): void {
    this.going.set(true);
    this.failed.set(false);
    const returnTo = window.location.pathname + window.location.search;
    this.injector.get(SsoService).ssoStepUp(returnTo).subscribe({
      next: (r) => this.assign(r.url),
      error: () => {
        this.going.set(false);
        this.failed.set(true);
      },
    });
  }

  protected submit(value: string): void {
    if (value) {
      this.request().answer(value);
    }
  }
}
