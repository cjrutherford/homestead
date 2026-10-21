import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ActivatedRoute, Router, convertToParamMap, provideRouter } from '@angular/router';
import { OperationState } from '../core/api.service';
import { WorkOrders } from '../core/work-orders.service';
import { SignIn } from './sign-in';

const SPEC = {
  authentik_url: 'https://authentik.lan',
  issuer: 'https://authentik.lan/application/o/homestead/',
  client_id: 'homestead',
  origins: ['http://127.0.0.1:8080', 'https://homestead.example.test'],
  redirect_uris: ['http://127.0.0.1:8080/api/v1/sso/callback', 'https://homestead.example.test/api/v1/sso/callback'],
  authorization_flow: 'default-provider-authorization-implicit-consent',
  invalidation_flow: 'default-provider-invalidation-flow',
  signing_key: 'authentik Self-signed Certificate',
  scopes: ['openid', 'profile', 'email'],
  authentik_version: '2026.8.3',
};

const NONE = {
  state: 'none',
  configured: false,
  linked: false,
  dormant: false,
  offered: false,
  client_secret_sealed: false,
  api_token_kept: false,
  defaults: { authentik_url: 'https://authentik.lan', origins: ['http://127.0.0.1:8080', 'https://homestead.example.test'] },
  authentik_installed: true,
  session_method: 'password',
};

const PLAN = {
  digest: 'abc123',
  spec: SPEC,
  objects: [
    { kind: 'OAuth2/OpenID provider', name: 'Homestead', action: 'create' },
    { kind: 'Application', name: 'Homestead (homestead)', action: 'create' },
  ],
  untouched: ['users and groups', 'policies and bindings'],
  warnings: ['The API token you give Homestead can do anything an Authentik administrator can.'],
  inspected: true,
  expires_at: '2026-10-19T12:30:00Z',
};

class FakeOrders {
  readonly state = signal<OperationState | null>(null);
  readonly orders = signal([]);
  stateOf(): OperationState | null {
    return this.state();
  }
}

describe('SignIn', () => {
  let fixture: ComponentFixture<SignIn>;
  let http: HttpTestingController;
  let orders: FakeOrders;

  async function open(query: Record<string, string> = {}): Promise<void> {
    orders = new FakeOrders();
    await TestBed.configureTestingModule({
      imports: [SignIn],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter([]),
        { provide: WorkOrders, useValue: orders },
        { provide: ActivatedRoute, useValue: { snapshot: { queryParamMap: convertToParamMap(query) } } },
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(SignIn);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  }

  afterEach(() => http.verify());

  const el = () => fixture.nativeElement as HTMLElement;
  const text = () => el().textContent ?? '';
  function status(state: object): void {
    http.expectOne('/api/v1/sso').flush(state);
    fixture.detectChanges();
  }
  function button(label: string): HTMLButtonElement {
    return Array.from(el().querySelectorAll('button')).find((b) => b.textContent?.includes(label)) as HTMLButtonElement;
  }
  function type(selector: string, value: string): void {
    const input = el().querySelector(selector) as HTMLInputElement;
    input.value = value;
    input.dispatchEvent(new Event('input'));
  }

  beforeEach(() => localStorage.clear());

  it('invites installing Authentik when it is not installed, and still allows planning', async () => {
    await open();
    status({ ...NONE, authentik_installed: false });
    expect(text()).toContain('Authentik is not installed yet');
    expect(el().querySelector('a[href="/catalog/authentik/install"]')?.textContent).toContain('Install Authentik');
    expect(el().querySelector('h1')?.textContent).toContain('Sign in with Authentik');
  });

  it('prefills the plan form from the appliance, not from the browser', async () => {
    await open();
    status(NONE);
    expect((el().querySelector('#sso-url') as HTMLInputElement).value).toBe('https://authentik.lan');
    expect((el().querySelector('#sso-origins') as HTMLTextAreaElement).value).toBe(
      'http://127.0.0.1:8080\nhttps://homestead.example.test',
    );
  });

  it('plans, reviews, approves by digest and follows the apply operation', async () => {
    await open();
    status(NONE);
    type('#sso-token', 'ak-token-123');
    type('#sso-origins', 'http://127.0.0.1:8080\nhttps://homestead.example.test');
    button('Review plan').click();
    const plan = http.expectOne('/api/v1/sso/plan');
    expect(plan.request.body).toEqual({
      authentik_url: 'https://authentik.lan',
      origins: ['http://127.0.0.1:8080', 'https://homestead.example.test'],
      api_token: 'ak-token-123',
    });
    plan.flush(PLAN);
    fixture.detectChanges();

    // The review lists what will be created, the exact redirect addresses,
    // what is left alone and what the token can do.
    expect(text()).toContain('OAuth2/OpenID provider');
    expect(text()).toContain('create');
    expect(text()).toContain('http://127.0.0.1:8080/api/v1/sso/callback');
    expect(text()).toContain('Left alone');
    expect(text()).toContain('users and groups');
    expect(text()).toContain('can do anything an Authentik administrator can');
    expect(text()).not.toContain('ak-token-123');

    // The token typed for the review is used again; it is not asked twice.
    expect(el().querySelector('#sso-approve-token')).toBeNull();
    expect(text()).toContain('uses the Authentik API token you entered above');
    (el().querySelector('input[name="keep-token"]') as HTMLInputElement).click();
    button('Approve and set up sign-in').click();
    const approve = http.expectOne('/api/v1/sso/approve');
    expect(approve.request.body).toEqual({ digest: 'abc123', api_token: 'ak-token-123', keep_token: true });
    approve.flush({ ...NONE, state: 'approved' });
    const apply = http.expectOne('/api/v1/sso/apply');
    expect(apply.request.method).toBe('POST');
    apply.flush({ id: 41 });
    fixture.detectChanges();
    expect(el().querySelector('app-operation-progress')).not.toBeNull();

    // The operation ends: the page reads the state again and says so.
    orders.state.set({ id: 41, type: 'sso.apply', status: 'succeeded', payload: '', result: '', error: '', created_at: '', updated_at: '', steps: [] });
    fixture.detectChanges();
    http.expectOne('/api/v1/sso').flush({ ...NONE, state: 'configured', configured: true, spec: SPEC, client_secret_sealed: true });
    fixture.detectChanges();
    expect(text()).toContain('Sign-in with Authentik is set up. Link your account to use it.');
    expect(text()).toContain('Set up, but not linked');
    // The link card on the configured page reads its own state.
    http.expectOne('/api/v1/sso').flush({ ...NONE, configured: true, spec: SPEC });
  });

  it('forgets the token when the review is cancelled', async () => {
    await open();
    status(NONE);
    button('Review plan').click();
    http.expectOne('/api/v1/sso/plan').flush({ ...PLAN, inspected: false });
    fixture.detectChanges();
    expect(text()).toContain('Authentik was not read');
    button('Cancel').click();
    fixture.detectChanges();
    expect(el().querySelector('#sso-url')).not.toBeNull();
  });

  it('shows the daemon\'s refusal of a plan in words', async () => {
    await open();
    status(NONE);
    button('Review plan').click();
    http.expectOne('/api/v1/sso/plan').flush('invalid request: Authentik has no address yet', { status: 422, statusText: 'Unprocessable' });
    fixture.detectChanges();
    expect(text()).toContain('Authentik has no address yet');
  });

  it('explains a cancelled password prompt without calling it a failure of the plan', async () => {
    await open();
    status(NONE);
    button('Review plan').click();
    http
      .expectOne('/api/v1/sso/plan')
      .flush('x', { status: 403, statusText: 'Forbidden', headers: { 'X-Homestead-Error': 'reauth-required' } });
    fixture.detectChanges();
    expect(text()).toContain('Nothing changed. This needs your local password.');
  });

  it('shows a configured, linked setup with the link card and the off switch', async () => {
    await open();
    status({ ...NONE, state: 'configured', configured: true, linked: true, offered: true, spec: SPEC, subject: 'sub-1', client_secret_sealed: true });
    expect(text()).toContain('The login page offers Sign in with Authentik');
    expect(text()).toContain('https://authentik.lan/application/o/homestead/');
    expect(text()).toContain('Sealed on this appliance');
    expect(text()).toContain('Forgotten after setup');
    expect(el().querySelector('app-sso-link')).not.toBeNull();
    // The link card loads its own state.
    http.expectOne('/api/v1/sso').flush({ ...NONE, configured: true, linked: true, subject: 'sub-1', spec: SPEC });
  });

  it('turns sign-in off only after a danger confirmation', async () => {
    await open();
    status({ ...NONE, state: 'configured', configured: true, linked: true, offered: true, spec: SPEC, subject: 'sub-1' });
    http.expectOne('/api/v1/sso').flush({ ...NONE, configured: true, linked: true, spec: SPEC });
    button('Turn off sign-in').click();
    fixture.detectChanges();
    const dialog = el().querySelector('dialog') as HTMLDialogElement;
    expect(dialog.classList.contains('dialog-danger')).toBe(true);
    expect(dialog.textContent).toContain('You can still sign in with your local password');
    http.expectNone('/api/v1/sso/disable');
    (dialog.querySelector('.danger') as HTMLButtonElement).click();
    const req = http.expectOne('/api/v1/sso');
    expect(req.request.method).toBe('DELETE');
    req.flush(NONE);
    fixture.detectChanges();
    expect(text()).toContain('Sign-in with Authentik is off. Your local password is unchanged.');
  });

  it('asks for a re-plan once the tunnel publishes the dashboard, and adds the public address', async () => {
    await open();
    const defaults = { ...NONE.defaults, public_origins: ['https://homestead.example.com'], public_authentik_url: 'https://auth.example.com' };
    status({
      ...NONE, state: 'configured', configured: true, linked: true, offered: true, spec: SPEC, subject: 'sub-1', defaults,
      replan: true, replan_reason: 'Sign-in does not yet cover the public dashboard address https://homestead.example.com.',
    });
    http.expectOne('/api/v1/sso').flush({ ...NONE, configured: true, linked: true, spec: SPEC });
    expect(text()).toContain('Re-plan sign-in for the public dashboard address');
    expect(text()).toContain('https://homestead.example.com');
    button('Re-plan sign-in').click();
    fixture.detectChanges();
    const origins = (el().querySelector('#sso-origins') as HTMLTextAreaElement).value.split('\n');
    expect(origins).toEqual([...SPEC.origins, 'https://homestead.example.com']);
  });

  it('shows the dormant state without a button promise', async () => {
    await open();
    status({ ...NONE, state: 'configured', configured: true, linked: true, dormant: true, spec: SPEC, subject: 's' });
    http.expectOne('/api/v1/sso').flush({ ...NONE, configured: true, linked: true, dormant: true, spec: SPEC });
    expect(text()).toContain('dormant');
  });

  const SETUP_DONE = { legacy: false, complete: true };

  it('shows what the callback reported', async () => {
    await open({ linked: '1' });
    http.expectOne('/api/v1/setup').flush(SETUP_DONE);
    status(NONE);
    expect(text()).toContain('Authentik is linked');
  });

  it('shows a failed link in words', async () => {
    await open({ sso_error: 'unlinked' });
    http.expectOne('/api/v1/setup').flush(SETUP_DONE);
    status(NONE);
    expect(text()).toContain('not linked to this appliance');
  });

  it('goes back to the setup wizard after a link started there', async () => {
    await open({ linked: '1' });
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    http.expectOne('/api/v1/setup').flush({ legacy: false, complete: false });
    status(NONE);
    expect(navigate).toHaveBeenCalledWith(['/setup'], {
      queryParams: { linked: '1', sso_error: undefined },
      replaceUrl: true,
    });
  });

  it('stays on the page once setup is complete', async () => {
    await open({ linked: '1' });
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigate');
    http.expectOne('/api/v1/setup').flush(SETUP_DONE);
    status(NONE);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('says so when setup failed', async () => {
    await open();
    status(NONE);
    button('Review plan').click();
    http.expectOne('/api/v1/sso/plan').flush(PLAN);
    fixture.detectChanges();
    button('Approve and set up sign-in').click();
    http.expectOne('/api/v1/sso/approve').flush({ ...NONE, state: 'approved' });
    http.expectOne('/api/v1/sso/apply').flush({ id: 9 });
    orders.state.set({ id: 9, type: 'sso.apply', status: 'failed', payload: '', result: '', error: 'Authentik has no signing key "x"', created_at: '', updated_at: '', steps: [] });
    fixture.detectChanges();
    http.expectOne('/api/v1/sso').flush(NONE);
    fixture.detectChanges();
    expect(text()).toContain('Setting up sign-in failed: Authentik has no signing key');
  });
});
