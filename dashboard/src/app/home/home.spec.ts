import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { App } from '../app';
import { SESSION_STORAGE_KEY } from '../auth/session.service';
import { routes } from '../app.routes';
import { Home } from './home';

const APPS = {
  apps: [
    { name: 'jellyfin', title: 'Jellyfin', icon: 'mdi:movie', role: 'user', url: 'https://homestead.lan/apps/jellyfin/' },
    { name: 'immich', title: 'Immich', icon: 'mdi:image', role: 'user', url: 'https://immich.homestead.lan/' },
  ],
};

describe('Home (S339)', () => {
  let http: HttpTestingController;

  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'sam', expiresAt: '' }));
    TestBed.configureTestingModule({
      imports: [Home],
      providers: [provideRouter(routes), provideHttpClient(), provideHttpClientTesting()],
    });
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => localStorage.clear());

  function open(me: object, apps: object): ComponentFixture<Home> {
    const fixture = TestBed.createComponent(Home);
    fixture.detectChanges();
    http.expectOne('/api/v1/me').flush(me);
    http.expectOne('/api/v1/me/apps').flush(apps);
    fixture.detectChanges();
    return fixture;
  }

  async function settled(fixture: ComponentFixture<Home>): Promise<HTMLElement> {
    await new Promise((resolve) => setTimeout(resolve, 400));
    fixture.detectChanges();
    return fixture.nativeElement as HTMLElement;
  }

  it('greets a person by name and shows a tile link per app, with no Administration link', async () => {
    const el = await settled(open({ role: 'person', display_name: 'Sam Rivera', username: 'sam' }, APPS));
    expect(el.querySelector('h1')?.textContent).toContain('Hello, Sam Rivera');
    const tiles = Array.from(el.querySelectorAll('a.tile'));
    expect(tiles.map((t) => t.getAttribute('href'))).toEqual([
      'https://homestead.lan/apps/jellyfin/',
      'https://immich.homestead.lan/',
    ]);
    expect(tiles[0].textContent).toContain('Jellyfin');
    expect(tiles[0].hasAttribute('target')).toBe(false);
    expect(el.textContent).not.toContain('Administration');
    expect(el.textContent).toContain('Sign out');
  });

  it('gives the owner an Administration link to /admin', async () => {
    const el = await settled(open({ role: 'owner', username: 'owner' }, APPS));
    expect(el.querySelector('h1')?.textContent).toContain('Hello, owner');
    const link = Array.from(el.querySelectorAll('a')).find((a) => a.textContent?.includes('Administration'));
    expect(link?.getAttribute('href')).toBe('/admin');
    expect(el.textContent).toContain('Sign out');
  });

  it('says so when nothing is shared yet', async () => {
    const el = await settled(open({ role: 'person', display_name: 'Sam' }, { apps: [] }));
    expect(el.textContent).toContain(
      'No apps are shared with you yet. Ask the owner of this Homestead to give you access.',
    );
    expect(el.querySelector('a.tile')).toBeNull();
  });

  it('calls only /me and /me/apps', () => {
    const fixture = open({ role: 'person', display_name: 'Sam' }, APPS);
    http.verify();
    expect(fixture.componentInstance).toBeTruthy();
  });
});

describe('the shell for a person (S339)', () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify({ username: 'sam', expiresAt: '' }));
    window.history.replaceState(null, '', '/');
    TestBed.configureTestingModule({
      imports: [App],
      providers: [provideRouter(routes), provideHttpClient(), provideHttpClientTesting()],
    });
  });

  afterEach(() => localStorage.clear());

  it('makes no owner API calls and shows no administration chrome', async () => {
    const http = TestBed.inject(HttpTestingController);
    const fixture = TestBed.createComponent(App);
    fixture.detectChanges();
    await fixture.whenStable();
    http.expectOne('/api/v1/me').flush({ role: 'person', display_name: 'Sam' });
    await new Promise((resolve) => setTimeout(resolve));
    fixture.detectChanges();
    for (const req of http.match(() => true)) {
      const url = req.request.url;
      expect(['/api/v1/me', '/api/v1/me/apps'], url).toContain(url);
      req.flush(url.endsWith('/apps') ? { apps: [] } : { role: 'person', display_name: 'Sam' });
    }
    fixture.detectChanges();
    http.verify();
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('aside.sidebar')).toBeNull();
    expect(el.querySelector('nav[aria-label="Primary"]')).toBeNull();
  });
});
