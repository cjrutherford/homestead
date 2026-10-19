import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { SsoLink, linkErrorMessage } from './sso-link';
import { HttpErrorResponse, HttpHeaders } from '@angular/common/http';

const BASE = {
  state: 'configured',
  configured: true,
  dormant: false,
  offered: false,
  client_secret_sealed: true,
  api_token_kept: false,
  defaults: { authentik_url: '', origins: [] },
  authentik_installed: true,
  session_method: 'password',
};

describe('SsoLink', () => {
  let fixture: ComponentFixture<SsoLink>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [SsoLink],
      providers: [provideHttpClient(), provideHttpClientTesting(), provideRouter([])],
    }).compileComponents();
    fixture = TestBed.createComponent(SsoLink);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => http.verify());

  const el = () => fixture.nativeElement as HTMLElement;
  function load(state: object): void {
    http.expectOne('/api/v1/sso').flush({ ...BASE, ...state });
    fixture.detectChanges();
  }
  function button(label: string): HTMLButtonElement {
    return Array.from(el().querySelectorAll('button')).find((b) => b.textContent?.includes(label)) as HTMLButtonElement;
  }

  it('points to the Sign-in page when sign-in is not set up', () => {
    load({ configured: false, linked: false });
    expect(el().textContent).toContain('not set up');
    expect(el().querySelector('a[href="/sign-in"]')).not.toBeNull();
    expect(button('Link Authentik')).toBeUndefined();
  });

  it('links: asks the daemon for the link address and sends the browser there', () => {
    load({ linked: false });
    const go = vi.spyOn(fixture.componentInstance as unknown as { go: (u: string) => void }, 'go').mockImplementation(() => undefined);
    button('Link Authentik').click();
    const req = http.expectOne('/api/v1/sso/link');
    expect(req.request.method).toBe('POST');
    req.flush({ url: 'https://authentik.lan/authorize?x=1', expires_at: '2099-01-01T00:00:00Z' });
    expect(go).toHaveBeenCalledWith('https://authentik.lan/authorize?x=1');
  });

  it('cancelling the unlink dialog changes nothing', () => {
    load({ linked: true, subject: 'abcdef0123456789abcdef0123456789' });
    button('Unlink Authentik').click();
    fixture.detectChanges();
    (Array.from(el().querySelectorAll('dialog button')).find((b) => b.textContent?.includes('Cancel')) as HTMLButtonElement).click();
    fixture.detectChanges();
    http.expectNone('/api/v1/sso/link');
    expect(el().querySelector('dialog')).toBeNull();
  });

  it('shows the linked account and unlinks, saying what unlinking does', () => {
    load({ linked: true, subject: 'abcdef0123456789abcdef0123456789' });
    expect(el().textContent).toContain('abcdef...456789');
    expect(el().textContent).toContain('signs out sessions that were started with Authentik');
    expect(el().textContent).toContain('local password still works');
    button('Unlink Authentik').click();
    fixture.detectChanges();
    // Nothing is sent until the owner confirms in the danger dialog.
    http.expectNone('/api/v1/sso/link');
    expect(el().querySelector('dialog')).not.toBeNull();
    expect(el().textContent).toContain('Your local password keeps working');
    const confirm = Array.from(el().querySelectorAll('dialog button')).find((b) => b.textContent?.includes('Unlink Authentik')) as HTMLButtonElement;
    confirm.click();
    const req = http.expectOne('/api/v1/sso/link');
    expect(req.request.method).toBe('DELETE');
    req.flush({ ...BASE, linked: false });
    fixture.detectChanges();
    expect(button('Link Authentik')).toBeDefined();
  });

  it('shows no error when the owner cancels the password prompt', () => {
    load({ linked: false });
    button('Link Authentik').click();
    http
      .expectOne('/api/v1/sso/link')
      .flush('enter your local password', {
        status: 403,
        statusText: 'Forbidden',
        headers: { 'X-Homestead-Error': 'reauth-required' },
      });
    fixture.detectChanges();
    expect(el().querySelector('[role="alert"]')).toBeNull();
  });

  it('says plainly when Authentik did not answer', () => {
    load({ linked: false });
    button('Link Authentik').click();
    http.expectOne('/api/v1/sso/link').flush('Authentik did not answer. Is it running?', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();
    expect(el().textContent).toContain('Authentik did not answer');
  });

  it('marks a dormant link', () => {
    load({ linked: true, dormant: true, subject: 'abc' });
    expect(el().textContent).toContain('dormant');
  });
});

describe('linkErrorMessage', () => {
  const err = (status: number, body: string, code?: string) =>
    new HttpErrorResponse({ status, error: body, headers: new HttpHeaders(code ? { 'X-Homestead-Error': code } : {}) });

  it('explains a lockout and a conflict', () => {
    expect(linkErrorMessage(err(429, '', 'reauth-locked'), 'Linking')).toContain('locked');
    expect(linkErrorMessage(err(409, 'an Authentik identity is already linked: unlink it first'), 'Linking')).toContain('already linked');
  });

  it('treats a cancelled prompt as no error', () => {
    expect(linkErrorMessage(err(403, 'x', 'reauth-invalid'), 'Linking')).toBeNull();
  });
});
