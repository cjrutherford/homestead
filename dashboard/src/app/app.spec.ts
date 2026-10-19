import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { App, NAV_GROUPS_KEY } from './app';
import { routes } from './app.routes';
import { SESSION_STORAGE_KEY } from './auth/session.service';

describe('App', () => {
  let fixture: ComponentFixture<App>;

  beforeEach(async () => {
    localStorage.clear();
    await TestBed.configureTestingModule({
      imports: [App],
      providers: [
        provideRouter(routes),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(App);
    fixture.detectChanges();
    // Drive navigation explicitly: the redirect is async and the status
    // page only fires its API calls once activated.
    await TestBed.inject(Router).navigateByUrl('/status');
    fixture.detectChanges();
    // The status page fires API calls on init; answer them.
    const http = TestBed.inject(HttpTestingController);
    for (const req of http.match(() => true)) {
      if (req.request.url === '/healthz') {
        req.flush({ status: 'ok', version: 'test' });
      } else if (req.request.url === '/api/v1/doctor') {
        req.flush({ overall: 'ok', checks: [] });
      } else {
        req.flush({ state: 'UNCLAIMED', version: 'test' });
      }
    }
    fixture.detectChanges();
    http.verify();
  });

  it('renders the brand and primary nav', () => {
    const text = fixture.nativeElement.textContent as string;
    expect(text).toContain('Homestead');
    const link = fixture.nativeElement.querySelector('nav a[href="/status"]');
    expect(link?.textContent).toContain('Status');
  });

  it('offers login in the sidebar foot when logged out', () => {
    const link = fixture.nativeElement.querySelector('.sidebar-foot a[href="/login"]');
    expect(link?.textContent).toContain('Log in');
    expect(fixture.nativeElement.querySelector('.sidebar-foot button')).toBeNull();
  });

  it('shows only Status and Claim to a logged-out visitor on an unclaimed appliance', () => {
    const links = Array.from(fixture.nativeElement.querySelectorAll('nav a')).map((a) =>
      (a as HTMLElement).textContent?.trim(),
    );
    expect(links).toEqual(['Status', 'Claim']);
    const groups = Array.from(fixture.nativeElement.querySelectorAll('.nav-group-toggle')).map(
      (b) => (b as HTMLElement).textContent?.trim(),
    );
    expect(groups).toEqual(['Overview']);
  });

  it('renders routed content in the outlet', () => {
    const text = fixture.nativeElement.textContent as string;
    expect(text).toContain('Unclaimed');
  });

  it('marks the current page and has one h1', () => {
    const current = fixture.nativeElement.querySelector('nav a[aria-current="page"]');
    expect(current?.textContent).toContain('Status');
    expect(fixture.nativeElement.querySelectorAll('h1').length).toBe(1);
  });

  it('starts with a skip link that focuses the main region', () => {
    const skip = fixture.nativeElement.querySelector('a.skip-link') as HTMLAnchorElement;
    expect(skip.textContent).toContain('Skip to content');
    expect(fixture.nativeElement.querySelector('.shell > :first-child')).toBe(skip);
    document.body.appendChild(fixture.nativeElement);
    const event = new MouseEvent('click', { bubbles: true, cancelable: true });
    skip.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(fixture.nativeElement.querySelector('main#main'));
    fixture.nativeElement.remove();
  });

  it('mounts the work-orders rail in its slot and renders nothing without orders', () => {
    const slot = fixture.nativeElement.querySelector('[data-slot="work-orders"]') as HTMLElement;
    expect(slot).not.toBeNull();
    expect(slot.querySelector('app-work-orders-rail')).not.toBeNull();
    expect(slot.querySelector('.rail')).toBeNull();
  });

  it('opens and closes the drawer with focus management and Escape', async () => {
    document.body.appendChild(fixture.nativeElement);
    const menu = fixture.nativeElement.querySelector('.menu-button') as HTMLButtonElement;
    const shell = fixture.nativeElement.querySelector('.shell') as HTMLElement;
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    menu.focus();
    menu.click();
    fixture.detectChanges();
    expect(menu.getAttribute('aria-expanded')).toBe('true');
    expect(shell.classList.contains('drawer-open')).toBe(true);
    expect(fixture.nativeElement.querySelector('main').hasAttribute('inert')).toBe(true);
    await new Promise((resolve) => setTimeout(resolve));
    expect(document.activeElement).toBe(fixture.nativeElement.querySelector('.drawer-close'));

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    fixture.detectChanges();
    expect(shell.classList.contains('drawer-open')).toBe(false);
    expect(fixture.nativeElement.querySelector('main').hasAttribute('inert')).toBe(false);
    expect(document.activeElement).toBe(menu);
    fixture.nativeElement.remove();
  });

  it('keeps Tab inside the open drawer', () => {
    document.body.appendChild(fixture.nativeElement);
    fixture.nativeElement.querySelector('.menu-button').click();
    fixture.detectChanges();
    const items = Array.from(
      fixture.nativeElement.querySelectorAll('.sidebar a[href], .sidebar button'),
    ) as HTMLElement[];
    const last = items[items.length - 1];
    last.focus();
    const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    last.dispatchEvent(tab);
    expect(tab.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(items[0]);
    fixture.nativeElement.remove();
  });
});

const COMPLETE_SETUP = {
  origin: 'fresh', legacy: false, complete: true, gated: false, current: '',
  steps: [], choices: {}, facts: {}, providers: [], access: [], starter_apps: [],
};

describe('App logged in', () => {
  async function setup(state: string): Promise<ComponentFixture<App>> {
    TestBed.resetTestingModule();
    await TestBed.configureTestingModule({
      imports: [App],
      providers: [provideRouter(routes), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    const fixture = TestBed.createComponent(App);
    fixture.detectChanges();
    TestBed.inject(HttpTestingController)
      .match('/api/v1/identity')
      .forEach((req) => req.flush({ state, version: 'test' }));
    fixture.detectChanges();
    return fixture;
  }

  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'owner', expiresAt: '' }));
  });

  afterEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute('data-theme');
  });

  it('shows the username with logout in the foot instead of login', async () => {
    const fixture = await setup('CLAIMED');
    const foot = fixture.nativeElement.querySelector('.sidebar-foot') as HTMLElement;
    expect(foot.textContent).toContain('owner');
    expect(foot.querySelector('a[href="/login"]')).toBeNull();
    expect(foot.querySelector('button')?.textContent).toContain('Log out');
  });

  it('groups every page under the five headings', async () => {
    const fixture = await setup('CLAIMED');
    const groups = Array.from(fixture.nativeElement.querySelectorAll('.nav-group')).map((g) => {
      const el = g as HTMLElement;
      return [
        el.querySelector('.nav-group-toggle')?.textContent?.trim(),
        Array.from(el.querySelectorAll('a')).map((a) => a.textContent?.trim()),
      ];
    });
    expect(groups).toEqual([
      ['Overview', ['Status']],
      ['Apps', ['Catalog', 'Craft apps', 'Craft files', 'Updates', 'Data']],
      ['Network', ['Edge', 'Exposure', 'Tunnel', 'Certificates', 'Trust', 'Firewall']],
      ['Storage and backups', ['Storage', 'Backups']],
      [
        'System',
        ['Configuration', 'Drift', 'Secrets', 'Sign-in', 'Operations', 'Audit', 'Account', 'Advanced'],
      ],
    ]);
  });

  async function visit(fixture: ComponentFixture<App>, url: string): Promise<void> {
    // The setup guard asks the daemon before a page opens (S287): answer
    // it, as a complete walkthrough, while the navigation is pending.
    const http = TestBed.inject(HttpTestingController);
    const navigation = TestBed.inject(Router).navigateByUrl(url);
    await new Promise((resolve) => setTimeout(resolve));
    http.match('/api/v1/setup').forEach((req) => req.flush(COMPLETE_SETUP));
    await navigation;
    fixture.detectChanges();
    http.match(() => true).forEach((req) => req.flush({}));
    fixture.detectChanges();
  }

  it('opens only the current group by default', async () => {
    const fixture = await setup('CLAIMED');
    await visit(fixture, '/advanced');
    const open = Array.from(fixture.nativeElement.querySelectorAll('.nav-group-toggle'))
      .filter((b) => (b as HTMLElement).getAttribute('aria-expanded') === 'true')
      .map((b) => (b as HTMLElement).textContent?.trim());
    expect(open).toEqual(['System']);
  });

  it('opens a group on request and remembers it', async () => {
    const fixture = await setup('CLAIMED');
    const toggle = fixture.nativeElement.querySelectorAll('.nav-group-toggle')[1] as HTMLElement;
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(fixture.nativeElement.querySelector(`#${toggle.getAttribute('aria-controls')}`).hidden).toBe(true);
    toggle.click();
    fixture.detectChanges();
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(JSON.parse(localStorage.getItem(NAV_GROUPS_KEY) ?? '{}')).toEqual({ apps: true });

    // A fresh shell reads the persisted state.
    const reloaded = await setup('CLAIMED');
    const again = reloaded.nativeElement.querySelectorAll('.nav-group-toggle')[1] as HTMLElement;
    expect(again.getAttribute('aria-expanded')).toBe('true');
  });

  it('reopens the current group even after it was closed', async () => {
    localStorage.setItem(NAV_GROUPS_KEY, JSON.stringify({ system: false }));
    const fixture = await setup('CLAIMED');
    await visit(fixture, '/advanced');
    const toggle = fixture.nativeElement.querySelectorAll('.nav-group-toggle')[4] as HTMLElement;
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
  });

  it('adds the Setup entry once the walkthrough status is known, and Finish setup for a legacy appliance', async () => {
    const fixture = await setup('CLAIMED');
    const link = () => fixture.nativeElement.querySelector('nav a[href="/setup"]') as HTMLElement | null;
    expect(link()).toBeNull();
    const http = TestBed.inject(HttpTestingController);
    http.match('/api/v1/setup').forEach((req) => req.flush({ ...COMPLETE_SETUP, complete: false, legacy: true, origin: 'legacy' }));
    fixture.detectChanges();
    expect(link()?.textContent?.trim()).toBe('Finish setup');
  });

  it('hides Claim once the appliance is claimed', async () => {
    const fixture = await setup('CLAIMED');
    expect(fixture.nativeElement.querySelector('nav a[href="/claim"]')).toBeNull();
  });

  it('applies a stored manual theme at startup', async () => {
    localStorage.setItem('homestead.theme', 'dark');
    await setup('CLAIMED');
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
  });
});
