import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { provideRouter } from '@angular/router';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { ApiService, AppSignInPlan, AppSignInStatus, OperationState } from '../core/api.service';
import { AppSignIn } from './app-sign-in';

function status(overrides: Partial<AppSignInStatus> = {}): AppSignInStatus {
  return { package: 'jellyfin', offered: true, enabled: false, replan: false, adapter: 'jellyfin-sso-plugin', token_kept: false, ...overrides };
}

const PLAN: AppSignInPlan = {
  package: 'jellyfin',
  title: 'Jellyfin',
  enabled: true,
  adapter: 'jellyfin-sso-plugin',
  provider: 'Homestead: Jellyfin',
  application: 'Jellyfin (homestead-jellyfin)',
  client_id: 'homestead-jellyfin',
  issuer: 'https://authentik.lan/application/o/homestead-jellyfin/',
  redirect_uris: ['https://jellyfin.lan/sso/OID/redirect/authentik'],
  app_settings: ['Install SSO Authentication 4.0.0.4'],
  changes: ['Create the provider and application in Authentik.'],
  warnings: ['A third-party plugin.'],
  digest: 'd1',
};

describe('AppSignIn (S298)', () => {
  let fixture: ComponentFixture<AppSignIn>;
  let http: HttpTestingController;
  let stream: Subject<OperationState>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AppSignIn],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    stream = new Subject<OperationState>();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockReturnValue(stream);
    fixture = TestBed.createComponent(AppSignIn);
    fixture.componentRef.setInput('name', 'jellyfin');
    fixture.componentRef.setInput('title', 'Jellyfin');
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  const el = () => fixture.nativeElement as HTMLElement;
  const text = () => (el().textContent ?? '').replace(/\s+/g, ' ');
  const button = (label: string) =>
    Array.from(el().querySelectorAll('button')).find((b) => b.textContent?.trim() === label);

  function load(s: AppSignInStatus) {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog/jellyfin/sign-in').flush(s);
    fixture.detectChanges();
  }

  it('reviews the plan, asks for a token once and follows the operation', () => {
    load(status({ plugin: { name: 'SSO Authentication', version: '4.0.0.4', url: 'u', sha256: 's' } }));
    expect(text()).toContain('SSO Authentication 4.0.0.4, a third-party Jellyfin plugin');
    button('Set up sign-in with Authentik')!.click();
    const plan = http.expectOne('/api/v1/catalog/jellyfin/sign-in/plan');
    expect(plan.request.body).toEqual({ enabled: true });
    plan.flush(PLAN);
    fixture.detectChanges();
    expect(text()).toContain('Homestead: Jellyfin');
    expect(text()).toContain('https://jellyfin.lan/sso/OID/redirect/authentik');
    expect(text()).toContain('Install SSO Authentication 4.0.0.4');
    expect(button('Approve')!.disabled).toBe(true);
    const token = el().querySelector('input[name="sign-in-token"]') as HTMLInputElement;
    token.value = 'ak-token';
    token.dispatchEvent(new Event('input'));
    fixture.detectChanges();
    button('Approve')!.click();
    const apply = http.expectOne('/api/v1/catalog/jellyfin/sign-in/apply');
    expect(apply.request.body).toEqual({ enabled: true, digest: 'd1', token: 'ak-token' });
    apply.flush({ id: 9 });
    fixture.detectChanges();
    expect(el().querySelector('input[name="sign-in-token"]')).toBeNull();
    stream.next({ id: 9, status: 'succeeded' } as OperationState);
    http.expectOne('/api/v1/catalog/jellyfin/sign-in').flush(status({ enabled: true, sign_in_url: 'https://jellyfin.lan/sso/OID/start/authentik' }));
    fixture.detectChanges();
    expect(text()).toContain('Sign-in with Authentik is on for Jellyfin.');
    expect(el().querySelector('a[href="https://jellyfin.lan/sso/OID/start/authentik"]')).not.toBeNull();
  });

  it('guides the owner when the app has not finished setting itself up, and offers to apply again (S312)', () => {
    load(status({ token_kept: true }));
    button('Set up sign-in with Authentik')!.click();
    http.expectOne('/api/v1/catalog/jellyfin/sign-in/plan').flush(PLAN);
    fixture.detectChanges();
    button('Approve')!.click();
    http.expectOne('/api/v1/catalog/jellyfin/sign-in/apply').flush({ id: 4 });
    stream.next({
      id: 4,
      status: 'failed',
      error: 'Jellyfin is not set up yet: it is still finishing its own installation. Wait a few minutes, open https://jellyfin.lan to check it is ready, then choose Apply sign-in again.',
    } as OperationState);
    http.expectOne('/api/v1/catalog/jellyfin/sign-in').flush(status({ token_kept: true }));
    fixture.detectChanges();
    expect(text()).toContain('still finishing its own installation');
    expect(el().querySelector('a[href="https://jellyfin.lan"]')).not.toBeNull();
    button('Apply sign-in again')!.click();
    expect(http.expectOne('/api/v1/catalog/jellyfin/sign-in/plan').request.body).toEqual({ enabled: true });
  });

  it('flags a re-plan, turns sign-in off without a token when one is kept, and reads a stale plan', () => {
    load(status({ enabled: true, replan: true, token_kept: true }));
    expect(text()).toContain('names changed since sign-in was set up');
    button('Turn sign-in off')!.click();
    http.expectOne('/api/v1/catalog/jellyfin/sign-in/plan').flush({ ...PLAN, enabled: false, digest: 'd2' });
    fixture.detectChanges();
    expect(el().querySelector('input[name="sign-in-token"]')).toBeNull();
    button('Approve')!.click();
    const apply = http.expectOne('/api/v1/catalog/jellyfin/sign-in/apply');
    expect(apply.request.body).toEqual({ enabled: false, digest: 'd2' });
    apply.flush('the plan changed since it was reviewed; review it again', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(text()).toContain('review it again');
    expect(button('Approve')).toBeUndefined();
  });

  it('says why sign-in is not available', () => {
    load(status({ offered: false, reason: 'Set up dashboard sign-in with Authentik first (Settings, Sign-in).' }));
    expect(text()).toContain('not available: Set up dashboard sign-in');
    expect(el().querySelector('a[href="/sign-in"]')).not.toBeNull();
    expect(button('Set up sign-in with Authentik')).toBeUndefined();
  });
});
