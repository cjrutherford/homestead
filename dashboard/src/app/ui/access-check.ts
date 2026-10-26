import { HttpErrorResponse } from '@angular/common/http';
import { Component, input, signal } from '@angular/core';
import { Observable } from 'rxjs';
import { AccessReport } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { StateMessage } from '../state/state-message';

/**
 * Reads back who can sign in (S314, ADR-043 §6): the groups and bindings
 * Homestead made, and anything that differs. It changes nothing; a
 * difference is reported, not put back. The Authentik token, when the
 * appliance kept none, is for this read only.
 */
@Component({
  selector: 'app-access-check',
  imports: [StateMessage],
  template: `
    <div class="access-check">
      <p>
        <button type="button" class="quiet" (click)="run()" [disabled]="busy()">Check who can sign in</button>
      </p>
      @if (needToken()) {
        <label>
          Authentik API token (used for this check only, not kept)
          <input type="password" name="access-token" autocomplete="off" [value]="token()" (input)="token.set($any($event.target).value)" />
        </label>
      }
      @if (error()) {
        <app-state-message kind="error" [headline]="error()!" />
      }
      @if (report(); as r) {
        @if (r.open) {
          <app-state-message kind="warning" headline="Open to every Authentik user." detail="No group is bound to this application. Plan sign-in again to close it." />
        }
        <ul aria-label="Groups Homestead made">
          @for (g of r.groups; track g.name) {
            <li>
              <span class="mono">{{ g.name }}</span>: {{ g.exists ? g.members + (g.members === 1 ? ' member' : ' members') + (g.owner_member ? ', you are in it' : '') : 'missing' }}
            </li>
          }
        </ul>
        <ul aria-label="Group bindings">
          @for (b of r.bindings; track b.group) {
            <li><span class="mono">{{ b.group }}</span>: {{ b.bound ? 'bound' : 'not bound' }}</li>
          }
        </ul>
        @for (d of r.drift; track d) {
          <p class="hint">Differs from what Homestead made: {{ d }}. Nothing was changed.</p>
        }
        @if (r.drift.length === 0 && !r.open) {
          <p class="hint">Matches what Homestead made.</p>
        }
      }
    </div>
  `,
})
export class AccessCheck {
  readonly check = input.required<(token: string) => Observable<AccessReport>>();

  protected readonly report = signal<AccessReport | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly needToken = signal(false);
  protected readonly token = signal('');

  protected run(): void {
    this.busy.set(true);
    this.error.set(null);
    this.check()(this.token().trim()).subscribe({
      next: (report) => {
        this.busy.set(false);
        this.report.set(report);
        this.token.set('');
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.report.set(null);
        const reason = typeof failure.error === 'string' ? failure.error.trim() : '';
        if (failure.status === 422 && reason.toLowerCase().includes('token')) {
          this.needToken.set(true);
        }
        this.error.set(httpErrorMessage(failure, 'Reading who can sign in', reason ? { 422: reason } : {}));
      },
    });
  }
}
