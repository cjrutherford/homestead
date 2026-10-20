import { Component, inject, input } from '@angular/core';
import { PasswordForm } from '../../account/password-form';
import { RecoveryIdentity } from '../../backups/recovery-identity';
import { SetupService, SetupStatus } from '../setup.service';
import { StepGuide } from './step-guide';

/**
 * Step 1: the password change and the recovery identity export, the same
 * components the Account and Backups pages use. Confirming the password is
 * the one thing recorded here (the daemon cannot tell a temporary one).
 */
@Component({
  imports: [PasswordForm, RecoveryIdentity, StepGuide],
  selector: 'app-setup-account-step',
  template: `
    <app-step-guide
      what="Backups are encrypted, and the recovery identity is the key that reads them if this appliance is lost. You also make sure the password that opens this dashboard is one only you know."
      outside="Do these on your own devices, not on the appliance."
    >
      <li>Keep your password in a password manager, so it is not lost and not reused.</li>
      <li>
        Store the exported recovery identity file offline, for example on a USB stick kept somewhere safe, not on this
        appliance.
      </li>
    </app-step-guide>

    <h3>Your password</h3>
    <p>
      If you were given a generated or temporary password, change it here. Homestead cannot tell, so confirm
      below once you have a password only you know.
    </p>
    <app-password-form />
    @if (status().facts.password_confirmed) {
      <p class="hint">Password confirmed.</p>
    } @else {
      <p>
        <button type="button" [disabled]="setup.busy()" (click)="confirmPassword()">Confirm my password</button>
      </p>
    }

    <app-recovery-identity />
  `,
})
export class AccountStep {
  protected readonly setup = inject(SetupService);
  readonly status = input.required<SetupStatus>();

  protected confirmPassword(): void {
    this.setup.change('account', { state: 'done' }, 'Password confirmed.');
  }
}
