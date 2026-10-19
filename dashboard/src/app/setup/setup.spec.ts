import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { Setup } from './setup';
import { setupStatus } from './setup.testing';
import { SetupService, SetupStatus } from './setup.service';

describe('Setup walkthrough (S287)', () => {
  let http: HttpTestingController;
  let fixture: ComponentFixture<Setup>;
  const el = () => fixture.nativeElement as HTMLElement;
  const text = () => el().textContent ?? '';

  async function open(status: SetupStatus = setupStatus()): Promise<void> {
    TestBed.configureTestingModule({
      imports: [Setup],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(Setup);
    fixture.detectChanges();
    http.expectOne('/api/v1/setup').flush(status);
    await fixture.whenStable();
    fixture.detectChanges();
  }

  const button = (label: string) =>
    Array.from(el().querySelectorAll('button, a.button')).find((b) =>
      (b.textContent ?? '').trim().includes(label),
    ) as HTMLElement | undefined;

  async function settle(): Promise<void> {
    await fixture.whenStable();
    fixture.detectChanges();
  }

  afterEach(() => http.verify());

  it('shows one heading, the four steps and the first step open', async () => {
    await open();
    expect(el().querySelectorAll('h1').length).toBe(1);
    expect(el().querySelector('h1')?.textContent).toContain('Set up your appliance');
    const names = Array.from(el().querySelectorAll('.step .step-name')).map((n) => n.textContent?.trim());
    expect(names).toEqual(['Secure the owner account', 'Sign-in', 'Domain and access', 'First apps']);
    // State is a word as well as a glyph.
    expect(el().querySelector('.step .step-status')?.textContent?.trim()).toBe('To do');
    expect(text()).toContain('Open the Backups page to export');
    expect(button('Open this step')).toBeDefined();
  });

  it('calls a legacy appliance Finish setup and says nothing is required', async () => {
    await open(setupStatus({ legacy: true, origin: 'legacy', gated: false }));
    expect(el().querySelector('h1')?.textContent).toContain('Finish setup');
    expect(text()).toContain('Nothing here is required');
  });

  it('skips a step through the setup route and offers to undo it', async () => {
    await open();
    button('Skip this step')!.click();
    const req = http.expectOne('/api/v1/setup/steps/account');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({ state: 'skipped' });
    const skipped = setupStatus();
    skipped.steps[0] = { ...skipped.steps[0], state: 'skipped', skipped: true };
    skipped.current = 'sign-in';
    req.flush(skipped);
    await settle();
    expect(el().querySelector('.step .step-status')?.textContent?.trim()).toBe('Skipped');
    button('Undo skip')!.click();
    const undo = http.expectOne('/api/v1/setup/steps/account');
    expect(undo.request.body).toEqual({ state: 'pending' });
    undo.flush(setupStatus());
  });

  it('shows the permission copy for the chosen provider and the tunnel only with Cloudflare', async () => {
    await open(setupStatus({ current: 'domain' }));
    expect(text()).toContain('Choose Cloudflare as the DNS provider to use a tunnel.');
    const tunnel = el().querySelector('input[name="access"][value="tunnel"]') as HTMLInputElement;
    expect(tunnel.disabled).toBe(true);
    (el().querySelector('input[name="provider"][value="cloudflare"]') as HTMLInputElement).click();
    const req = http.expectOne('/api/v1/setup/steps/domain');
    expect(req.request.body).toEqual({ provider: 'cloudflare' });
    const chosen = setupStatus({ current: 'domain', choices: { provider: 'cloudflare' } });
    chosen.access = chosen.access.map((a) => (a.id === 'tunnel' ? { ...a, available: true, reason: undefined } : a));
    req.flush(chosen);
    await settle();
    expect(text()).toContain('Zone: DNS: Edit');
    expect(text()).toContain('list, create and delete records');
    expect((el().querySelector('input[name="access"][value="tunnel"]') as HTMLInputElement).disabled).toBe(false);
  });

  it('seals a credential through the secret route, clears the form, and never puts it in a setup request', async () => {
    await open(setupStatus({ current: 'domain', choices: { provider: 'cloudflare' } }));
    const input = el().querySelector('input[name="api_token"]') as HTMLInputElement;
    expect(input.type).toBe('password');
    input.value = 'TOKEN-VALUE-123';
    el().querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }));
    const req = http.expectOne('/api/v1/tls/issuance/credentials');
    expect(req.request.body).toEqual({ provider: 'cloudflare', values: { api_token: 'TOKEN-VALUE-123' } });
    req.flush({ provider: 'cloudflare', stored: ['api_token'] });
    expect(input.value).toBe('');
    // The status is read again; the reply names presence only.
    const next = setupStatus({ current: 'domain', choices: { provider: 'cloudflare' } });
    next.facts.domain.credentials_sealed = true;
    http.expectOne('/api/v1/setup').flush(next);
    await settle();
    expect(text()).not.toContain('TOKEN-VALUE-123');
    expect(text()).toContain('Credentials are stored');
  });

  it('refuses an empty credential form without calling the daemon', async () => {
    await open(setupStatus({ current: 'domain', choices: { provider: 'cloudflare' } }));
    el().querySelector('form')!.dispatchEvent(new Event('submit', { cancelable: true }));
    await settle();
    http.expectNone('/api/v1/tls/issuance/credentials');
    expect(text()).toContain('Fill in every credential field.');
  });

  it('opens an existing domain or tunnel as a review that changes nothing', async () => {
    const status = setupStatus({ current: 'domain' });
    status.facts.review_existing = true;
    status.facts.tunnels = 1;
    await open(status);
    expect(text()).toContain('A domain, tunnel or exposure plan is already set up here.');
    expect(text()).toContain('Nothing is changed or redone.');
    button('This looks right')!.click();
    const req = http.expectOne('/api/v1/setup/steps/domain');
    expect(req.request.body).toEqual({ state: 'done' });
    req.flush(status);
    http.expectNone('/api/v1/tls/issuance/credentials');
  });

  it('confirms the password from step 1 with its own button beside the Account link', async () => {
    await open();
    expect(text()).toContain('Change password on the Account page');
    button('Confirm my password')!.click();
    const req = http.expectOne('/api/v1/setup/steps/account');
    expect(req.request.body).toEqual({ state: 'done' });
    req.flush(setupStatus());
  });

  it('explains what a tunnel or router choice still needs instead of offering to finish', async () => {
    await open(setupStatus({ current: 'domain', choices: { provider: 'cloudflare', access: 'tunnel' } }));
    expect(text()).toContain('No tunnel exists yet.');
    expect(button('I have finished here')).toBeUndefined();
    expect(button('Check again')).toBeDefined();
  });

  it('offers to finish a tunnel choice once the tunnel exists', async () => {
    const status = setupStatus({ current: 'domain', choices: { provider: 'cloudflare', access: 'tunnel' } });
    status.facts.tunnels = 1;
    await open(status);
    expect(button('I have finished here')).toBeDefined();
  });

  it('asks for an approved exposure plan before finishing a router choice', async () => {
    await open(setupStatus({ current: 'domain', choices: { provider: 'manual', access: 'router' } }));
    expect(text()).toContain('No exposure plan is approved yet.');
    expect(button('I have finished here')).toBeUndefined();
  });

  it('links each starter app to the install wizard with a way back', async () => {
    await open(setupStatus({ current: 'apps' }));
    const link = Array.from(el().querySelectorAll('a')).find((a) => a.textContent?.includes('Install Nextcloud'))!;
    expect(link.getAttribute('href')).toBe('/catalog/nextcloud/install?from=setup');
  });

  it('says when setup is complete and keeps the steps reachable', async () => {
    const done = setupStatus({ complete: true, gated: false, current: '' });
    done.steps = done.steps.map((s) => ({ ...s, state: 'done' as const }));
    await open(done);
    expect(text()).toContain('Setup is complete.');
    expect(button('Review this step')).toBeDefined();
  });

  it('keeps the page usable when the status cannot be read', async () => {
    TestBed.configureTestingModule({
      imports: [Setup],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(Setup);
    fixture.detectChanges();
    http.expectOne('/api/v1/setup').flush('down', { status: 500, statusText: 'Server Error' });
    await settle();
    expect(text()).toContain('Setup status could not be read.');
    expect(TestBed.inject(SetupService).status()).toBeNull();
    button('Try again')!.click();
    http.expectOne('/api/v1/setup').flush(setupStatus());
  });
});
