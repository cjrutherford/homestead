import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import {
  HttpErrorResponse,
  HttpInterceptorFn,
  HttpRequest,
  HttpResponse,
  provideHttpClient,
  withInterceptors,
} from '@angular/common/http';
import { provideRouter } from '@angular/router';
import { Observable, of, throwError } from 'rxjs';
import { StorageSetup } from '../storage/storage';
import { Setup } from './setup';
import { setupRefreshInterceptor } from './setup-refresh.interceptor';
import { SetupService, SetupStatus, SetupStepChange, SetupStepName, SetupStepState } from './setup.service';
import { setupStatus } from './setup.testing';

interface Call {
  method: string;
  url: string;
  body: unknown;
}

const MOUNT = {
  ID: 1, Device: '/dev/sdb1', Mountpoint: '/mnt/data', FSType: 'ext4', Options: null, ReadOnly: false, UUID: 'u1',
  Label: 'disk', Accessible: true, TotalBytes: 2 * 1024 ** 4, FreeBytes: 1024 ** 4, AvailBytes: 1024 ** 4,
};
const IDENTITY = { created: true, recipient: 'age1appliance', fingerprint: 'aa11', exported: false, acknowledged: false };
const SSO_NONE = {
  state: 'none', configured: false, linked: false, dormant: false, offered: false, client_secret_sealed: false,
  api_token_kept: false, authentik_installed: false, session_method: 'password',
  defaults: { authentik_url: 'https://authentik.lan', origins: [] },
};

describe('Setup wizard (S291)', () => {
  let fixture: ComponentFixture<Setup>;
  let calls: Call[];
  let status: SetupStatus;
  /** Canned answers for the pages the steps embed; a function may also change `status`. */
  let routes: Record<string, unknown | ((call: Call) => unknown)>;

  const el = () => fixture.nativeElement as HTMLElement;
  const text = () => el().textContent ?? '';
  const heading = () => el().querySelector('h2')?.textContent?.replace(/\s+/g, ' ').trim();
  const button = (label: string) =>
    Array.from(el().querySelectorAll('button, a.button')).find((b) => (b.textContent ?? '').trim() === label) as
      | HTMLButtonElement
      | undefined;
  const wrote = (method: string, url: string) =>
    calls.filter((c) => c.method === method && c.url === url);

  async function settle(): Promise<void> {
    for (let i = 0; i < 3; i++) {
      await fixture.whenStable();
      fixture.detectChanges();
    }
  }

  function reply(body: unknown): Observable<HttpResponse<unknown>> {
    return of(new HttpResponse({ status: 200, body }));
  }

  /** Mirrors the daemon just enough: a step change moves that step's state and the current step. */
  function applyStep(name: string, change: SetupStepChange): void {
    const step = status.steps.find((s) => s.name === name)!;
    if (change.state) {
      step.state = change.state as SetupStepState;
      step.skipped = change.state === 'skipped';
    }
    if (change.provider !== undefined) {
      status.choices = { ...status.choices, provider: change.provider };
    }
    if (change.access !== undefined) {
      status.choices = { ...status.choices, access: change.access };
    }
    status.current = (status.steps.find((s) => s.state === 'pending')?.name ?? '') as SetupStepName | '';
    status.complete = status.current === '';
  }

  const backend: HttpInterceptorFn = (req: HttpRequest<unknown>) => {
    const call = { method: req.method, url: req.url, body: req.body };
    calls.push(call);
    if (req.url === '/api/v1/setup' && req.method === 'GET') {
      return reply(structuredClone(status));
    }
    const stepRoute = /^\/api\/v1\/setup\/steps\/([^/]+)$/.exec(req.url);
    if (stepRoute && req.method === 'PUT') {
      applyStep(decodeURIComponent(stepRoute[1]), req.body as SetupStepChange);
      return reply(structuredClone(status));
    }
    const canned = routes[`${req.method} ${req.url}`];
    if (canned !== undefined) {
      return reply(typeof canned === 'function' ? canned(call) : canned);
    }
    return throwError(() => new HttpErrorResponse({ status: 404, url: req.url, statusText: 'Not Found' }));
  };

  async function open(initial: SetupStatus = setupStatus()): Promise<void> {
    status = initial;
    TestBed.configureTestingModule({
      imports: [Setup],
      providers: [
        provideRouter([]),
        provideHttpClient(withInterceptors([setupRefreshInterceptor, backend])),
      ],
    });
    fixture = TestBed.createComponent(Setup);
    fixture.detectChanges();
    await settle();
  }

  /** A status whose first `n` steps are done. */
  function doneThrough(n: number, overrides: Partial<SetupStatus> = {}): SetupStatus {
    const s = setupStatus(overrides);
    s.steps.forEach((step, i) => {
      if (i < n) {
        step.state = 'done';
      }
    });
    s.current = (s.steps.find((step) => step.state === 'pending')?.name ?? '') as SetupStepName | '';
    s.complete = s.current === '';
    return s;
  }

  beforeEach(() => {
    calls = [];
    routes = {
      'GET /api/v1/backups/identity': IDENTITY,
      'GET /api/v1/storage/mounts': [MOUNT],
      'GET /api/v1/storage/registrations': [],
      'GET /api/v1/storage/assignments': [],
      'GET /api/v1/sso': SSO_NONE,
    };
  });

  afterEach(() => TestBed.resetTestingModule());

  describe('navigation', () => {
    it('shows one heading, the five steps in order and the first step open', async () => {
      await open();
      expect(el().querySelectorAll('h1').length).toBe(1);
      expect(el().querySelector('h1')?.textContent).toContain('Set up your appliance');
      const names = Array.from(el().querySelectorAll('.step .step-name')).map((n) => n.textContent?.trim());
      expect(names).toEqual(['Secure the owner account', 'Storage', 'Sign-in', 'Domain and access', 'First apps']);
      expect(heading()).toBe('Step 1 of 5: Secure the owner account');
      // State is a word as well as a glyph.
      expect(el().querySelector('.step .step-status')?.textContent?.trim()).toBe('To do');
    });

    it('marks the current step in the progress indicator with aria-current', async () => {
      await open();
      const items = Array.from(el().querySelectorAll('ol[aria-label="Setup progress"] > li'));
      expect(items.map((i) => i.getAttribute('aria-current'))).toEqual(['step', null, null, null, null]);
    });

    it('opens at the first pending step, not at step one', async () => {
      await open(doneThrough(1));
      expect(heading()).toBe('Step 2 of 5: Storage');
      const items = Array.from(el().querySelectorAll('ol[aria-label="Setup progress"] > li'));
      expect(items[1].getAttribute('aria-current')).toBe('step');
    });

    it('opens on the summary when nothing is pending', async () => {
      await open(doneThrough(5));
      expect(heading()).toBe('Summary');
      expect(text()).toContain('Setup is complete.');
      expect(wrote('GET', '/api/v1/backups/identity').length).toBe(0);
    });

    it('keeps Next disabled until the step is done or skipped, and Back disabled on the first step', async () => {
      await open();
      expect(button('Next')!.disabled).toBe(true);
      expect(button('Back')!.disabled).toBe(true);
      expect(text()).toContain('Next unlocks when this step is done');
      button('Skip this step')!.click();
      await settle();
      expect(wrote('PUT', '/api/v1/setup/steps/account')[0].body).toEqual({ state: 'skipped' });
      expect(button('Next')!.disabled).toBe(false);
      expect(button('Undo skip')).toBeDefined();
    });

    it('shows a waived storage step as not required, with no Undo skip', async () => {
      const s = doneThrough(1);
      s.steps[1].state = 'skipped';
      s.steps[1].waived = true;
      s.current = 'sign-in';
      await open(s);
      button('Back')!.click();
      await settle();
      expect(text()).toContain('Not required: setup was finished before this step existed');
      expect(button('Undo skip')).toBeUndefined();
      expect(button('Skip this step')).toBeUndefined();
    });

    it('moves forward on Next and always back on Back, and moves focus to the new heading', async () => {
      await open(doneThrough(1));
      expect(button('Next')!.disabled).toBe(true);
      button('Skip this step')!.click();
      await settle();
      button('Next')!.click();
      await settle();
      expect(heading()).toBe('Step 3 of 5: Sign-in');
      expect(document.activeElement).toBe(el().querySelector('h2'));
      button('Back')!.click();
      await settle();
      expect(heading()).toBe('Step 2 of 5: Storage');
      expect(document.activeElement).toBe(el().querySelector('h2'));
      // Back works even though the step it returns to is still to do.
      expect(button('Next')!.disabled).toBe(false);
    });

    it('leads to a summary of every outcome and back to a skipped step', async () => {
      const s = doneThrough(4);
      s.steps[2].state = 'skipped';
      s.steps[2].skipped = true;
      s.steps[3].state = 'done';
      s.steps[4].state = 'pending';
      s.current = 'apps';
      await open(s);
      expect(heading()).toBe('Step 5 of 5: First apps');
      button('Skip this step')!.click();
      await settle();
      expect(button('Next: summary')).toBeDefined();
      button('Next: summary')!.click();
      await settle();
      expect(heading()).toBe('Summary');
      const items = Array.from(el().querySelectorAll('ul[aria-label="Outcome of each step"] > li')).map((li) =>
        (li.textContent ?? '').replace(/\s+/g, ' ').trim(),
      );
      expect(items.length).toBe(5);
      expect(items[2]).toContain('Skipped');
      expect(items[0]).toContain('Done');
      button('Finish this step')!.click();
      await settle();
      expect(heading()).toBe('Step 3 of 5: Sign-in');
    });

    it('calls a legacy appliance Finish setup and says nothing is required', async () => {
      await open(setupStatus({ legacy: true, origin: 'legacy', gated: false }));
      expect(el().querySelector('h1')?.textContent).toContain('Finish setup');
      expect(text()).toContain('Nothing here is required');
    });

    it('keeps the page usable when the status cannot be read', async () => {
      status = setupStatus();
      TestBed.configureTestingModule({
        imports: [Setup],
        providers: [
          provideRouter([]),
          provideHttpClient(
            withInterceptors([(() => throwError(() => new HttpErrorResponse({ status: 500 }))) as HttpInterceptorFn]),
          ),
        ],
      });
      fixture = TestBed.createComponent(Setup);
      fixture.detectChanges();
      await settle();
      expect(text()).toContain('Setup status could not be read.');
      expect(TestBed.inject(SetupService).status()).toBeNull();
      expect(button('Try again')).toBeDefined();
    });
  });

  describe('account step', () => {
    it('explains the step with its outside checklist and embeds the password form and recovery identity', async () => {
      await open();
      expect(text()).toContain('What this does.');
      expect(text()).toContain('Outside Homestead');
      const tasks = Array.from(el().querySelectorAll('ol.outside-tasks > li')).map((li) => li.textContent);
      expect(tasks.length).toBe(2);
      expect(tasks[0]).toContain('password manager');
      expect(tasks[1]).toContain('offline');
      expect(el().querySelector('app-password-form form')).not.toBeNull();
      expect(wrote('GET', '/api/v1/backups/identity').length).toBe(1);
      expect(el().querySelector('app-recovery-identity')).not.toBeNull();
    });

    it('changes the password through the account route', async () => {
      routes['POST /api/v1/admins/password'] = {};
      await open();
      const set = (id: string, v: string) => ((el().querySelector('#' + id) as HTMLInputElement).value = v);
      set('account-current', 'old-password-1');
      set('account-new', 'new-password-12');
      set('account-confirm', 'new-password-12');
      el().querySelector('app-password-form form')!.dispatchEvent(new Event('submit'));
      await settle();
      expect(wrote('POST', '/api/v1/admins/password')[0].body).toEqual({
        current_password: 'old-password-1',
        new_password: 'new-password-12',
      });
    });

    it('exports the recovery identity only after confirmation, and acknowledges it', async () => {
      routes['POST /api/v1/backups/identity/export'] = {
        identity: 'AGE-SECRET-KEY-TEST', recipient: 'age1appliance', fingerprint: 'aa11', exported_at: 'now', actor: 'owner',
      };
      routes['POST /api/v1/backups/identity/ack'] = {};
      await open();
      button('Export recovery identity')!.click();
      await settle();
      expect(wrote('POST', '/api/v1/backups/identity/export').length).toBe(0);
      button('Confirm export')!.click();
      await settle();
      expect(wrote('POST', '/api/v1/backups/identity/export')[0].body).toEqual({ confirm: true });
      expect(text()).toContain('AGE-SECRET-KEY-TEST');
      button('I have stored this key offline')!.click();
      await settle();
      expect(wrote('POST', '/api/v1/backups/identity/ack').length).toBe(1);
      expect(text()).not.toContain('AGE-SECRET-KEY-TEST');
    });

    it('confirms the password with its own button', async () => {
      await open();
      button('Confirm my password')!.click();
      await settle();
      expect(wrote('PUT', '/api/v1/setup/steps/account')[0].body).toEqual({ state: 'done' });
    });
  });

  describe('storage step', () => {
    it('embeds the storage page with its checklist and an fstab example', async () => {
      await open(doneThrough(1));
      expect(el().querySelector('app-storage')).not.toBeNull();
      // The embedded page leaves the heading to the wizard.
      expect(el().querySelectorAll('h1').length).toBe(1);
      expect(text()).toContain('already be mounted');
      expect(text()).toContain('/etc/fstab');
      expect(text()).toContain('UUID=');
      expect(wrote('GET', '/api/v1/storage/mounts').length).toBe(1);
    });

    it('unlocks Next once the app class is assigned through the storage route', async () => {
      routes['POST /api/v1/storage/assignments'] = () => {
        status.facts.storage_app_assigned = true;
        applyStep('storage', { state: 'done' });
        return { Class: 'application', Mountpoint: '/mnt/data', Actor: 'owner', CreatedAt: '' };
      };
      await open(doneThrough(1));
      expect(button('Next')!.disabled).toBe(true);
      const page = fixture.debugElement.query(By.directive(StorageSetup)).componentInstance as StorageSetup;
      page.assign('application', '/mnt/data', 'owner');
      await settle();
      expect(wrote('POST', '/api/v1/storage/assignments')[0].body).toMatchObject({ class: 'application', mountpoint: '/mnt/data' });
      // The wizard read the status again by itself.
      expect(wrote('GET', '/api/v1/setup').length).toBeGreaterThan(1);
      expect(el().querySelector('.step[aria-current="step"] .step-status')?.textContent?.trim()).toBe('Done');
      expect(button('Next')!.disabled).toBe(false);
    });

    it('skips and undoes the skip through the setup route', async () => {
      await open(doneThrough(1));
      button('Skip this step')!.click();
      await settle();
      expect(wrote('PUT', '/api/v1/setup/steps/storage')[0].body).toEqual({ state: 'skipped' });
      button('Undo skip')!.click();
      await settle();
      expect(wrote('PUT', '/api/v1/setup/steps/storage')[1].body).toEqual({ state: 'pending' });
    });
  });

  describe('sign-in step', () => {
    it('lists the outside tasks in order with the exact initial-setup address and token menu path', async () => {
      await open(doneThrough(2));
      const tasks = Array.from(el().querySelectorAll('ol.outside-tasks > li')).map((li) =>
        (li.textContent ?? '').replace(/\s+/g, ' ').trim(),
      );
      expect(tasks.length).toBe(4);
      expect(tasks[0]).toContain('authentik.lan');
      expect(tasks[1]).toContain('root certificate');
      expect(el().querySelector('a[href="/trust"]')).not.toBeNull();
      expect(tasks[2]).toContain('https://authentik.lan/if/flow/initial-setup/');
      expect(tasks[2]).toContain('akadmin');
      expect(tasks[3]).toContain('Admin interface, Directory, Tokens and App passwords');
      expect(tasks[3]).toContain('Create');
      expect(tasks[3]).toContain('API');
    });

    it('shows guidance, not a fake address, when the sign-in defaults are not known', async () => {
      delete routes['GET /api/v1/sso'];
      await open(doneThrough(2));
      expect(text()).toContain('the Authentik address shown in the Sign-in section below');
      expect(text()).not.toContain('authentik.<name>');
      expect(el().querySelector('a[href*="initial-setup"]')).toBeNull();
    });

    it('offers the install wizard for Authentik first and reads its package', async () => {
      await open(doneThrough(2));
      expect(el().querySelector('app-install-wizard')).not.toBeNull();
      expect(wrote('GET', '/api/v1/catalog/authentik').length).toBe(1);
      expect(el().querySelector('app-sign-in')).toBeNull();
    });

    it('goes straight to the sign-in page when Authentik is already installed', async () => {
      const s = doneThrough(2);
      s.facts.sign_in.authentik_installed = true;
      routes['GET /api/v1/sso'] = { ...SSO_NONE, authentik_installed: true };
      await open(s);
      expect(el().querySelector('app-install-wizard')).toBeNull();
      expect(el().querySelector('app-sign-in')).not.toBeNull();
      // Its own heading is left to the step.
      expect(el().querySelectorAll('h1').length).toBe(1);
    });
  });

  describe('domain step', () => {
    it('pins the Cloudflare permissions it shows to the provider copy', async () => {
      await open(doneThrough(3));
      const tasks = Array.from(el().querySelectorAll('ol.outside-tasks > li')).map((li) => li.textContent ?? '');
      expect(tasks.length).toBe(4);
      expect(tasks[0]).toContain('Cloudflare');
      expect(tasks[1]).toContain('Zone resources');
      expect(tasks[1]).toContain('Zone: Read');
      expect(tasks[1]).toContain('DNS: Edit');
      expect(tasks[1]).toContain('Account resources');
      expect(tasks[1]).toContain('Cloudflare Tunnel: Edit');
      expect(tasks[3]).toContain('records');
    });

    it('records the provider choice and gates the tunnel on Cloudflare', async () => {
      await open(doneThrough(3));
      expect(text()).toContain('Choose Cloudflare as the DNS provider to use a tunnel.');
      expect((el().querySelector('input[name="access"][value="tunnel"]') as HTMLInputElement).disabled).toBe(true);
      (el().querySelector('input[name="provider"][value="cloudflare"]') as HTMLInputElement).click();
      await settle();
      expect(wrote('PUT', '/api/v1/setup/steps/domain')[0].body).toEqual({ provider: 'cloudflare' });
    });

    it('seals a credential through the secret route, clears the form, and never puts it in a setup request', async () => {
      routes['POST /api/v1/tls/issuance/credentials'] = { provider: 'cloudflare', stored: ['api_token'] };
      await open(doneThrough(3, { choices: { provider: 'cloudflare' } }));
      const input = el().querySelector('input[name="api_token"]') as HTMLInputElement;
      expect(input.type).toBe('password');
      input.value = 'TOKEN-VALUE-123';
      input.form!.dispatchEvent(new Event('submit', { cancelable: true }));
      await settle();
      expect(wrote('POST', '/api/v1/tls/issuance/credentials')[0].body).toEqual({
        provider: 'cloudflare',
        values: { api_token: 'TOKEN-VALUE-123' },
      });
      expect(input.value).toBe('');
      expect(JSON.stringify(calls.filter((c) => c.url.startsWith('/api/v1/setup')))).not.toContain('TOKEN-VALUE-123');
      expect(text()).not.toContain('TOKEN-VALUE-123');
    });

    it('refuses an empty credential form without calling the daemon', async () => {
      await open(doneThrough(3, { choices: { provider: 'cloudflare' } }));
      el().querySelector('app-setup-domain-step form')!.dispatchEvent(new Event('submit', { cancelable: true }));
      await settle();
      expect(wrote('POST', '/api/v1/tls/issuance/credentials').length).toBe(0);
      expect(text()).toContain('Fill in every credential field.');
    });

    it('embeds the tunnel page for a tunnel choice and explains what is still needed', async () => {
      await open(doneThrough(3, { choices: { provider: 'cloudflare', access: 'tunnel' } }));
      expect(el().querySelector('app-tunnel')).not.toBeNull();
      expect(wrote('GET', '/api/v1/tunnel/status').length).toBe(1);
      expect(text()).toContain('No tunnel exists yet.');
      expect(button('I have finished here')).toBeUndefined();
      expect(el().querySelectorAll('h1').length).toBe(1);
    });

    it('embeds the exposure page for a router choice and asks for an approved plan', async () => {
      await open(doneThrough(3, { choices: { provider: 'manual', access: 'router' } }));
      expect(el().querySelector('app-exposure')).not.toBeNull();
      expect(wrote('GET', '/api/v1/exposure/review').length).toBe(1);
      expect(text()).toContain('No exposure plan is approved yet.');
      expect(button('I have finished here')).toBeUndefined();
    });

    it('offers to finish a tunnel choice once the tunnel exists', async () => {
      const s = doneThrough(3, { choices: { provider: 'cloudflare', access: 'tunnel' } });
      s.facts.tunnels = 1;
      await open(s);
      button('I have finished here')!.click();
      await settle();
      expect(wrote('PUT', '/api/v1/setup/steps/domain')[0].body).toEqual({ state: 'done' });
    });

    it('opens an existing domain as a review that changes nothing', async () => {
      const s = doneThrough(3);
      s.facts.review_existing = true;
      s.facts.tunnels = 1;
      await open(s);
      expect(text()).toContain('A domain, tunnel or exposure plan is already set up here.');
      expect(text()).toContain('Nothing is changed or redone.');
      button('This looks right')!.click();
      await settle();
      expect(wrote('PUT', '/api/v1/setup/steps/domain')[0].body).toEqual({ state: 'done' });
      expect(wrote('POST', '/api/v1/tls/issuance/credentials').length).toBe(0);
    });

    it('stays on the home network with one click', async () => {
      await open(doneThrough(3));
      button('Keep home network only')!.click();
      await settle();
      expect(wrote('PUT', '/api/v1/setup/steps/domain')[0].body).toEqual({ state: 'done' });
    });
  });

  describe('apps step', () => {
    it('opens the install wizard for a chosen app and returns to the list', async () => {
      await open(doneThrough(4));
      expect(el().querySelector('ul[aria-label="Starter apps"]')).not.toBeNull();
      button('Install Nextcloud')!.click();
      await settle();
      expect(el().querySelector('app-install-wizard')).not.toBeNull();
      expect(wrote('GET', '/api/v1/catalog/nextcloud').length).toBe(1);
      // The package could not be read here; the way back leaves the wizard.
      button('Back to the app list')?.click();
    });

    it('records that the owner is finished with apps', async () => {
      await open(doneThrough(4));
      button('I am finished with apps')!.click();
      await settle();
      expect(wrote('PUT', '/api/v1/setup/steps/apps')[0].body).toEqual({ state: 'done' });
    });
  });
});
