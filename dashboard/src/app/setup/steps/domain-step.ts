import { HttpErrorResponse } from '@angular/common/http';
import { Component, computed, inject, input, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { ApiService } from '../../core/api.service';
import { httpErrorMessage } from '../../core/http-error';
import { ToastService } from '../../core/toast.service';
import { Exposure } from '../../exposure/exposure';
import { StateMessage } from '../../state/state-message';
import { Tunnel } from '../../tunnel/tunnel';
import { SetupService, SetupStatus } from '../setup.service';
import { StepGuide } from './step-guide';

/**
 * Step 4: provider and credential, then home only, Cloudflare Tunnel or a
 * router port forward. The tunnel and the exposure plan are the Tunnel and
 * Exposure pages, embedded, so the plans are reviewed and approved here with
 * the same routes. The only secret typed here is the DNS credential, which
 * goes straight to the sealed secret route and is cleared at once.
 */
@Component({
  imports: [Exposure, RouterLink, StateMessage, StepGuide, Tunnel],
  selector: 'app-setup-domain-step',
  template: `
    <app-step-guide
      what="Choose where your domain's DNS lives and how the appliance is reached. The default is your home network only: nothing is open to the internet. Tunnel and port forward plans are shown for your approval before anything is created."
      outside="Only needed if you want the appliance reachable by a domain name. Skip this step to stay on your home network."
    >
      <li>
        Cloudflare: add your domain to Cloudflare (Websites, Add a site) and point the registrar's nameservers at the two
        Cloudflare gives you.
      </li>
      <li>
        Cloudflare: create a scoped API token (My Profile, API Tokens, Create Token, Custom token), limited to the one
        zone, with exactly these permissions, then paste it below:
        <ul>
          @for (perm of cloudflarePermissions(); track perm.grant) {
            <li>
              <strong>{{ perm.grant }}</strong> to {{ perm.reason }}.
            </li>
          }
        </ul>
      </li>
      <li>
        Other providers need the equivalent credential:
        <ul>
          @for (p of otherProviders(); track p.name) {
            <li>
              <strong>{{ p.label }}</strong
              >: {{ p.summary }}
              @for (perm of p.permissions ?? []; track perm.grant) {
                {{ perm.grant }};
              }
            </li>
          }
        </ul>
      </li>
      <li>
        Manual DNS: you enter the records yourself at your registrar. Review the exposure plan below; its DNS records
        table lists each record's type, name and value.
      </li>
    </app-step-guide>

    @if (s().facts.review_existing) {
      <app-state-message
        kind="notice"
        headline="A domain, tunnel or exposure plan is already set up here."
        detail="Review it and confirm it is right. Nothing is changed or redone."
      />
      <p>
        <a class="button" routerLink="/tls">Review certificates</a>
      </p>
    }

    @if (setup.error() || error()) {
      <app-state-message kind="error" [headline]="setup.error() || error()" />
    }

    <h3>DNS provider</h3>
    <p>Optional. Pick where your domain's DNS lives, or leave it and stay on your home network.</p>
    <fieldset [disabled]="setup.busy()">
      <legend>DNS provider</legend>
      @for (p of s().providers; track p.name) {
        <label>
          <input
            type="radio"
            name="provider"
            [value]="p.name"
            [checked]="s().choices.provider === p.name"
            (change)="chooseProvider(p.name)"
          />
          {{ p.label }}
        </label>
      }
    </fieldset>

    @if (provider(); as p) {
      <p>{{ p.summary }}</p>
      @if (p.permissions?.length) {
        <p>Give the credential only these permissions:</p>
        <ul>
          @for (perm of p.permissions; track perm.grant) {
            <li>
              <strong>{{ perm.grant }}</strong> to {{ perm.reason }}.
            </li>
          }
          @if (s().choices.access === 'tunnel') {
            @for (perm of p.tunnel_permissions; track perm.grant) {
              <li>
                <strong>{{ perm.grant }}</strong> to {{ perm.reason }}.
              </li>
            }
          }
        </ul>
      }
      @for (note of p.notes ?? []; track note) {
        <p class="hint">{{ note }}</p>
      }
      @if (p.credentials?.length) {
        <form #creds (submit)="$event.preventDefault(); saveCredentials(creds)" autocomplete="off">
          @for (field of p.credentials; track field.name) {
            <p>
              <label>
                {{ field.label }}
                <input
                  [name]="field.name"
                  [type]="field.secret ? 'password' : 'text'"
                  autocomplete="off"
                  spellcheck="false"
                />
              </label>
            </p>
          }
          <p class="hint">
            {{
              s().facts.domain.credentials_sealed
                ? 'Credentials are stored. Enter new ones to replace them.'
                : 'Stored sealed on this appliance. They cannot be read back.'
            }}
          </p>
          <button type="submit" [disabled]="setup.busy() || busy()">Store credentials</button>
        </form>
      }
    }

    <h3>How the appliance is reached</h3>
    <fieldset [disabled]="setup.busy()">
      <legend>Access</legend>
      @for (opt of s().access; track opt.id) {
        <label>
          <input
            type="radio"
            name="access"
            [value]="opt.id"
            [disabled]="!opt.available"
            [checked]="s().choices.access === opt.id"
            (change)="chooseAccess(opt.id)"
          />
          {{ opt.label }}
        </label>
        <p class="hint">{{ opt.available ? opt.description : opt.reason }}</p>
      }
    </fieldset>

    @switch (s().choices.access) {
      @case ('tunnel') {
        <h3>Cloudflare Tunnel</h3>
        <p>You review the plan before anything is created. Use the form below, then check again.</p>
        <app-tunnel [embedded]="true" />
      }
      @case ('router') {
        <h3>Router port forward</h3>
        <p>
          Plan which apps are public. You review and approve the exact ports and records before anything opens.
        </p>
        <app-exposure [embedded]="true" />
      }
    }

    @if (needs(); as why) {
      <app-state-message kind="notice" headline="Not finished yet." [detail]="why" />
      <p>
        <button type="button" (click)="refresh()">Check again</button>
      </p>
    } @else {
      <p>
        <button type="button" [disabled]="setup.busy()" (click)="confirm()">{{ confirmLabel() }}</button>
      </p>
    }
  `,
})
export class DomainStep {
  protected readonly setup = inject(SetupService);
  private readonly api = inject(ApiService);
  private readonly toast = inject(ToastService);

  readonly status = input.required<SetupStatus>();
  protected readonly s = this.status;
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  protected readonly provider = computed(() => {
    const status = this.status();
    return status.providers.find((p) => p.name === status.choices.provider) ?? null;
  });
  protected readonly cloudflarePermissions = computed(() => {
    const cf = this.status().providers.find((p) => p.name === 'cloudflare');
    return [...(cf?.permissions ?? []), ...(cf?.tunnel_permissions ?? [])];
  });
  protected readonly otherProviders = computed(() => this.status().providers.filter((p) => p.name !== 'cloudflare'));

  /**
   * What a tunnel or router port forward still needs before this step may be
   * confirmed, or null when it may (always for home network only). Mirrors
   * the daemon's rule, which answers 422 otherwise.
   */
  protected readonly needs = computed<string | null>(() => {
    const status = this.status();
    const provider = status.choices.provider || status.facts.domain.provider || '';
    switch (status.choices.access) {
      case 'tunnel':
        if (provider !== 'cloudflare') {
          return 'Choose Cloudflare as the DNS provider above first.';
        }
        return status.facts.tunnels > 0
          ? null
          : 'No tunnel exists yet. Create and approve it in the Cloudflare Tunnel form above, then check again.';
      case 'router':
        if (!provider) {
          return 'Choose a DNS provider above first.';
        }
        return status.facts.exposures > 0
          ? null
          : 'No exposure plan is approved yet. Review and approve one in the Router port forward section above, then check again.';
      default:
        return null;
    }
  });
  protected readonly confirmLabel = computed(() => {
    const s = this.status();
    if (s.facts.review_existing) {
      return 'This looks right';
    }
    return s.choices.access === 'tunnel' || s.choices.access === 'router' ? 'I have finished here' : 'Keep home network only';
  });

  protected refresh(): void {
    this.setup.setupStatus().subscribe();
  }

  protected chooseProvider(name: string): void {
    this.setup.change('domain', { provider: name });
  }

  protected chooseAccess(id: string): void {
    this.setup.change('domain', { access: id });
  }

  protected confirm(): void {
    this.setup.change('domain', { state: 'done' }, 'Domain and access confirmed.');
  }

  /**
   * Seals the DNS credential typed into the form. The values leave the form
   * straight for the secret route and the form is cleared either way.
   */
  protected saveCredentials(form: HTMLFormElement): void {
    const provider = this.provider();
    if (!provider || this.busy()) {
      return;
    }
    const data = new FormData(form);
    const values: Record<string, string> = {};
    for (const field of provider.credentials ?? []) {
      values[field.name] = String(data.get(field.name) ?? '');
    }
    if (Object.values(values).some((value) => value === '')) {
      this.error.set('Fill in every credential field.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.tlsIssuanceCredentials(provider.name, values).subscribe({
      next: () => {
        form.reset();
        this.busy.set(false);
        this.toast.show(`Stored the ${provider.label} credentials. They cannot be read back.`);
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        form.reset();
        this.busy.set(false);
        this.error.set(httpErrorMessage(failure, 'the credentials'));
      },
    });
  }
}
