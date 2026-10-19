import { Component, input } from '@angular/core';
import { ReauthRequest } from '../core/reauth';
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
  `,
})
export class ReauthDialog {
  readonly request = input.required<ReauthRequest>();

  protected submit(value: string): void {
    if (value) {
      this.request().answer(value);
    }
  }
}
