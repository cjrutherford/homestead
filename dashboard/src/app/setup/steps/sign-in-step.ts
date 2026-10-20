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
        Make
        @if (base()) {
          <code>{{ host() }}</code>
        } @else {
          the Authentik address
        }
        reachable from the device you are using now: add it to your router's local DNS, or add a hosts-file entry
        pointing at the same address this dashboard answers on (the address <code>{{ dashboardHost }}</code> resolves to).
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
          the Authentik address shown in the Sign-in section below once Authentik is installed
        }
        (the address itself, not a page under it). Authentik takes you to its first-run page: enter an email and a
        password for the <code>akadmin</code> administrator and keep the Base URL it shows. Authentik offers this only
        until the account exists. Right after the install it can answer "Request has been denied" or "Not found" for
        about a minute while it finishes starting; reload the page.
      </li>
      <li>
        Signed in as <code>akadmin</code>, open Admin interface, Directory, Tokens and App passwords, and choose New
        Token. Give it an identifier (for example <code>homestead</code>), choose the user <code>akadmin</code>, keep
        API Token selected, and create it. Then use the copy button on its row and paste the key into the form below.
        The token expires by default; that is fine, Homestead only needs it while you review and approve. (Checked
        against Authentik 2026.8, the version the catalog pins, on 2026-10-20.)
      </li>
    </app-step-guide>

    @if (installOpen()) {
      <h3>Install Authentik</h3>
      <app-install-wizard
        [embedded]="true"
        appName="authentik"
        closeLabel="Continue to sign-in"
        (closed)="installOpen.set(false)"
      />
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

  protected readonly host = () => (this.base() ? new URL(this.base()).host : '');
  /** Authentik 2026.8 starts its first-run only from the address itself (S292). */
  protected readonly initialSetup = () => `${this.base().replace(/\/+$/, '')}/`;
  protected readonly dashboardHost = typeof location === 'undefined' ? 'homestead.local' : location.hostname;

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
