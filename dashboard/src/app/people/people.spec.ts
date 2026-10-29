import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { People } from './people';

const SAM = { id: 4, display_name: 'Sam Lee', username: 'sam', email: 'sam@example.com', state: 'active', origin: 'created', grants: [{ app: 'immich', role: 'user' }], status: 'ok', drift: null };
const PAT = {
  id: 5, display_name: 'Pat Doe', username: 'pat', email: '', state: 'active', origin: 'created', grants: [], status: 'drifted',
  drift: [{ kind: 'grant', app: 'immich', homestead: 'user', authentik: 'none' }],
};
const GONE = { id: 6, display_name: 'Gone Away', username: 'gone', email: '', state: 'active', origin: 'created', grants: [], status: 'missing', drift: null };

const VIEW = {
  token_kept: true, owner: 'chris', apps: ['immich', 'jellyfin'], token_words: 'It can create people.',
  people: [SAM, PAT, GONE], unmanaged: [{ username: 'old', name: 'Old One', email: '', active: true }],
};

describe('People', () => {
  let fixture: ComponentFixture<People>;
  let http: HttpTestingController;

  const el = () => fixture.nativeElement as HTMLElement;
  const text = () => el().textContent ?? '';
  const button = (label: string) => Array.from(el().querySelectorAll('button')).find((b) => b.textContent?.trim() === label) as HTMLButtonElement | undefined;

  function start(view: object = VIEW) {
    TestBed.configureTestingModule({ providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()] });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(People);
    fixture.detectChanges();
    http.expectOne('/api/v1/people').flush(view);
    fixture.detectChanges();
  }

  beforeEach(() => undefined);
  afterEach(() => http.verify());

  it('lists the owner with every app, people with their lamps, and the unmanaged', () => {
    start();
    expect(text()).toContain('chris');
    expect(text()).toContain('The owner always has every app');
    expect(el().querySelectorAll('.person-owner .lamp-on').length).toBe(2);
    expect(text()).toContain('Sam Lee');
    const samLamps = Array.from(el().querySelectorAll('.person[data-status="ok"] .lamp'));
    expect(samLamps.map((l) => l.classList.contains('lamp-on'))).toEqual([true, false]);
    expect(text()).toContain('In Authentik, not managed here');
    expect(text()).toContain('Old One');
    expect(text()).toContain('never Homestead administrators');
  });

  it('adopts an unmanaged account after one click, with confirm', () => {
    start();
    button('Adopt')!.click();
    const req = http.expectOne('/api/v1/people/adopt');
    expect(req.request.body).toEqual({ username: 'old', confirm: true });
    req.flush({ person: { ...SAM, display_name: 'Old One' } });
    http.expectOne('/api/v1/people').flush(VIEW);
  });

  it('explains the no-token state, what the token can do, and offers the opt-in', () => {
    start({ token_kept: false, people: [], unmanaged: [], apps: [], owner: 'chris', reason: 'no-token', token_words: 'It can create people and the homestead- groups.' });
    expect(text()).toContain('Homestead cannot manage people yet');
    expect(text()).toContain('It can create people and the homestead- groups.');
    expect(el().querySelector('a[href="/sign-in"]')).not.toBeNull();
    expect(button('Add a person')!.disabled).toBe(true);
    expect(el().querySelector('.people-list')).toBeNull();
  });

  it('says sign-in is not set up when there is no connection at all', () => {
    start({ token_kept: false, people: [], unmanaged: [], apps: [], owner: 'chris', reason: 'no-sign-in', token_words: 'x' });
    expect(text()).toContain('Sign-in with Authentik is not set up');
  });

  it('adds a person with chosen apps and shows the generated password once', () => {
    start();
    button('Add a person')!.click();
    fixture.detectChanges();
    const set = (name: string, value: string) => {
      const input = el().querySelector(`input[name="${name}"]`) as HTMLInputElement;
      input.value = value;
      input.dispatchEvent(new Event('input'));
    };
    set('name', 'New Person');
    set('username', 'newp');
    const generated = el().querySelectorAll('input[name="how"]')[1] as HTMLInputElement;
    generated.dispatchEvent(new Event('change'));
    const select = el().querySelector('select[aria-label="Access to jellyfin"]') as HTMLSelectElement;
    select.value = 'admin';
    select.dispatchEvent(new Event('change'));
    fixture.detectChanges();
    expect(text()).toContain('does not make them a Homestead administrator');
    button('Add person')!.click();
    const req = http.expectOne('/api/v1/people');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({
      display_name: 'New Person', username: 'newp', email: '', method: 'generated',
      grants: [{ app: 'jellyfin', role: 'admin' }], confirm: true,
    });
    req.flush({ person: { ...SAM, display_name: 'New Person' }, revealed: { kind: 'password', value: 'Secret-Pass-123' } });
    http.expectOne('/api/v1/people').flush(VIEW);
    fixture.detectChanges();
    expect(text()).toContain('Secret-Pass-123');
    expect(text()).toContain('cannot show it again');
    button('I have it')!.click();
    fixture.detectChanges();
    expect(text()).not.toContain('Secret-Pass-123');
  });

  function addSam() {
    button('Add a person')!.click();
    fixture.detectChanges();
    for (const [name, value] of [['name', 'Sam Lee'], ['username', 'sam']]) {
      const input = el().querySelector(`input[name="${name}"]`) as HTMLInputElement;
      input.value = value;
      input.dispatchEvent(new Event('input'));
    }
    fixture.detectChanges();
    button('Add person')!.click();
    return http.expectOne((r) => r.url === '/api/v1/people' && r.method === 'POST');
  }

  it('keeps the add dialog open when sign-in was not set up, and offers a generated password once', () => {
    start();
    addSam().flush({
      person: SAM, revealed: null, sign_in_failed: true,
      warning: 'sam was added, but how they sign in was not set up (a missing recovery flow). Reset their password to try again.',
    });
    http.expectOne('/api/v1/people').flush(VIEW);
    fixture.detectChanges();
    expect(text()).toContain('Sam Lee was added');
    expect(text()).toContain('Their sign-in was not set up.');
    expect(text()).toContain('a missing recovery flow');
    button('Generate a password instead')!.click();
    const req = http.expectOne('/api/v1/people/4/password-reset');
    expect(req.request.body).toEqual({ method: 'generated', password: '', confirm: true });
    req.flush({ revealed: { kind: 'password', value: 'Fresh-Pass-456' } });
    http.expectOne('/api/v1/people').flush(VIEW);
    fixture.detectChanges();
    expect(text()).toContain('Fresh-Pass-456');
    expect(text()).toContain('cannot show it again');
    expect(button('Generate a password instead')).toBeUndefined();
  });

  it('shows why generating failed and stays on the added person', () => {
    start();
    addSam().flush({ person: SAM, revealed: null, sign_in_failed: true, warning: 'sam was added, but how they sign in was not set up (x).' });
    http.expectOne('/api/v1/people').flush(VIEW);
    fixture.detectChanges();
    button('Generate a password instead')!.click();
    http.expectOne('/api/v1/people/4/password-reset').flush('Authentik refused the password', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();
    expect(text()).toContain('Authentik refused the password');
    expect(button('Generate a password instead')).toBeDefined();
    button('Close')!.click();
    fixture.detectChanges();
    expect(text()).not.toContain('Sam Lee was added');
  });

  it('closes the add dialog with a notice when only a grant failed', () => {
    start();
    addSam().flush({ person: SAM, revealed: { kind: 'link', value: 'https://auth.example/recover/x' }, warning: 'sam was added, but immich could not be granted (x).' });
    http.expectOne('/api/v1/people').flush(VIEW);
    fixture.detectChanges();
    expect(text()).toContain('https://auth.example/recover/x');
    expect(button('Generate a password instead')).toBeUndefined();
  });

  it('shows a one-time link for the local network and one for the internet', () => {
    start();
    addSam().flush({
      person: SAM,
      revealed: { kind: 'link', value: 'https://homestead.lan/apps/auth/if/flow/r/?flow_token=x', public_value: 'https://home.example.com/apps/auth/if/flow/r/?flow_token=x' },
    });
    http.expectOne('/api/v1/people').flush(VIEW);
    fixture.detectChanges();
    expect(text()).toContain('On the local network');
    expect(text()).toContain('https://homestead.lan/apps/auth/if/flow/r/?flow_token=x');
    expect(text()).toContain('Over the internet');
    expect(text()).toContain('https://home.example.com/apps/auth/if/flow/r/?flow_token=x');
    expect(button('Copy local link')).toBeDefined();
    expect(button('Copy public link')).toBeDefined();
  });

  it('says there is no public link yet when Authentik has no public address', () => {
    start();
    addSam().flush({ person: SAM, revealed: { kind: 'link', value: 'https://homestead.lan/apps/auth/if/flow/r/?flow_token=x' } });
    http.expectOne('/api/v1/people').flush(VIEW);
    fixture.detectChanges();
    expect(text()).toContain('There is no public link yet');
    expect(button('Copy public link')).toBeUndefined();
  });

  it('grants and revokes only what changed, as separate confirmed calls', () => {
    start();
    (Array.from(el().querySelectorAll('.person[data-status="ok"] button')).find((b) => b.textContent?.trim() === 'Apps') as HTMLButtonElement).click();
    fixture.detectChanges();
    const immich = el().querySelector('select[aria-label="Access to immich"]') as HTMLSelectElement;
    expect(immich.value).toBe('user');
    immich.value = 'none';
    immich.dispatchEvent(new Event('change'));
    const jelly = el().querySelector('select[aria-label="Access to jellyfin"]') as HTMLSelectElement;
    jelly.value = 'admin';
    jelly.dispatchEvent(new Event('change'));
    button('Save apps')!.click();
    const revoke = http.expectOne('/api/v1/people/4/apps/immich/revoke');
    expect(revoke.request.body).toEqual({ confirm: true });
    revoke.flush({ person: SAM });
    const grant = http.expectOne('/api/v1/people/4/apps/jellyfin/admin');
    expect(grant.request.method).toBe('PUT');
    grant.flush({ person: SAM });
    http.expectOne('/api/v1/people').flush(VIEW);
  });

  it('resolves drift on request only: Put back is first, Keep is the alternative', () => {
    start();
    expect(text()).toContain('Nothing has been changed');
    expect(text()).toContain('Access to immich');
    expect(text()).toContain('Put back is recommended');
    const labels = Array.from(el().querySelectorAll('.person[data-status="drifted"] .person-drift button')).map((b) => b.textContent!.trim());
    expect(labels).toEqual(['Put back', 'Keep what Authentik has']);
    http.expectNone('/api/v1/people/5/resolve');
    button('Put back')!.click();
    const req = http.expectOne('/api/v1/people/5/resolve');
    expect(req.request.body).toEqual({ action: 'put-back', method: 'link', confirm: true });
    req.flush({ revealed: null });
    http.expectOne('/api/v1/people').flush(VIEW);
  });

  it('offers Recreate or Remove from Homestead for a person gone from Authentik', () => {
    start();
    expect(text()).toContain('was deleted in Authentik');
    button('Recreate')!.click();
    const recreate = http.expectOne('/api/v1/people/6/resolve');
    expect(recreate.request.body.action).toBe('recreate');
    recreate.flush({ revealed: { kind: 'link', value: 'https://x/recover/abc' } });
    http.expectOne('/api/v1/people').flush(VIEW);
    fixture.detectChanges();
    expect(text()).toContain('https://x/recover/abc');
    button('I have it')!.click();
    fixture.detectChanges();
    button('Remove from Homestead')!.click();
    fixture.detectChanges();
    http.expectNone('/api/v1/people/6/resolve');
    const confirm = Array.from(el().querySelectorAll('dialog button')).find((b) => b.textContent?.trim() === 'Remove from Homestead') as HTMLButtonElement;
    confirm.click();
    http.expectOne('/api/v1/people/6/resolve').flush({ revealed: null });
    http.expectOne('/api/v1/people').flush(VIEW);
  });

  it('asks before removing and says app data stays', () => {
    start();
    (Array.from(el().querySelectorAll('.person[data-status="ok"] button')).find((b) => b.textContent?.trim() === 'Remove') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(text()).toContain('App data stays in the apps');
    http.expectNone('/api/v1/people/4/remove');
    const confirm = Array.from(el().querySelectorAll('dialog button')).find((b) => b.textContent?.trim() === 'Remove') as HTMLButtonElement;
    confirm.click();
    const req = http.expectOne('/api/v1/people/4/remove');
    expect(req.request.body).toEqual({ confirm: true });
    req.flush({ ok: true });
    http.expectOne('/api/v1/people').flush(VIEW);
  });

  it('shows the reason in the dialog when a change fails, and changes nothing else', () => {
    start();
    (Array.from(el().querySelectorAll('.person[data-status="ok"] button')).find((b) => b.textContent?.trim() === 'Disable') as HTMLButtonElement).click();
    fixture.detectChanges();
    (Array.from(el().querySelectorAll('dialog button')).find((b) => b.textContent?.trim() === 'Disable') as HTMLButtonElement).click();
    http.expectOne('/api/v1/people/4/disable').flush('Authentik could not do that: refused', { status: 502, statusText: 'Bad Gateway' });
    fixture.detectChanges();
    expect(text()).toContain('Authentik could not do that: refused');
  });
});
