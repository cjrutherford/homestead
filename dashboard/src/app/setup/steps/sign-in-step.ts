import { Component, OnInit, inject, input, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { InstallWizard } from '../../catalog/install-wizard';
import { SignIn } from '../../sso/sign-in';
import { SsoService } from '../../sso/sso.service';
import { SetupStatus } from '../setup.service';
import { StepGuide } from './step-guide';

/**
 * Step 3: install Authentik through the install wizard (live operation
 * progress), then the Sign-in page's plan, approve, apply and link. Both are
 * the existing components, embedded.
 */
@Component({
  imports: [InstallWizard, RouterLink, SignIn, StepGuide],
  selector: 'app-setup-sign-in-step',
  template: `
    <app-step-guide
      what="Authentik lets you sign in to Homestead and your apps with one account. Installing it is optional, and your local owner password always works, even if Authentik is gone (the break-glass way in)."
      outside="Authentik has its own first-run and its own admin screens. Do these in order on a device on your home network."
    >
      <li>
        Make <code>{{ host() }}</code> reachable from the device you are using now: add it to your router's local DNS, or
        add a hosts-file entry pointing at this appliance's address.
      </li>
      <li>
        Trust the appliance's root certificate on this device, so the browser accepts the address
        (<a routerLink="/trust" target="_blank">open Trust</a>).
      </li>
      <li>
        Open
        @if (base()) {
          <a [href]="initialSetup()" target="_blank" rel="noopener">{{ initialSetup() }}</a>
        } @else {
          <code>{{ initialSetup() }}</code>
        }
        and create the
        <code>akadmin</code> account. Authentik only offers this page until the account exists.
      </li>
      <li>
        In Authentik, sign in as <code>akadmin</code>, then open Admin interface, Directory, Tokens and App passwords,
        and choose Create. Set the intent to API, create it, copy the key, and paste it into the form below when it asks
        for the API token. (Menu path checked against Authentik 2025.x on 2026-10-20.)
      </li>
    </app-step-guide>

    @if (installOpen()) {
      <h3>Install Authentik</h3>
      <app-install-wizard [embedded]="true" appName="authentik" (closed)="installOpen.set(false)" />
    } @else {
      <app-sign-in [embedded]="true" />
    }
  `,
})
export class SignInStep implements OnInit {
  private readonly sso = inject(SsoService);
  readonly status = input.required<SetupStatus>();

  /** Open until the owner finishes the install wizard; it is skipped when Authentik is already installed. */
  protected readonly installOpen = signal(false);
  protected readonly base = signal('');

  protected readonly host = () => (this.base() ? new URL(this.base()).host : 'authentik.<name>');
  protected readonly initialSetup = () =>
    `${this.base() ? this.base().replace(/\/+$/, '') : 'https://authentik.<name>'}/if/flow/initial-setup/`;

  ngOnInit(): void {
    this.installOpen.set(!this.status().facts.sign_in.authentik_installed);
    this.sso.ssoStatus().subscribe({
      next: (state) => {
        const url = state.defaults.authentik_url;
        try {
          this.base.set(url ? new URL(url).origin : '');
        } catch {
          this.base.set('');
        }
      },
      error: () => this.base.set(''),
    });
  }
}
