import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Catalog } from './catalog';
import { InstallDrafts } from './install-drafts';

const PACKAGES = [
  {
    name: 'jellyfin',
    version: '12.2',
    title: 'Jellyfin',
    blurb: 'Media server you control.',
    category: 'media',
    icon: 'mdi:plex',
  },
  {
    name: 'gridcraft',
    version: '0.7.0',
    title: 'GridCraft',
    blurb: 'Spreadsheet that runs in your browser.',
    category: 'productivity',
    icon: 'x',
    installed: true,
  },
];

const MANIFEST = {
  Name: 'jellyfin',
  Version: '12.2',
  Title: 'Jellyfin',
  Blurb: 'Media server you control.',
  Description: 'Streams media.',
  Category: 'media',
  Services: [{ Name: 'jellyfin', Image: 'jellyfin/jellyfin:12.2', Exposes: 'web' }],
  Requirements: {
    MemoryMB: 2048,
    Storage: [{ Class: 'bulk', MinBytes: 1 }],
    Ports: [8096],
  },
  Exposure: { Package: 'jellyfin', Endpoints: [] },
  BackupPaths: ['config'],
};

describe('Catalog', () => {
  let fixture: ComponentFixture<Catalog>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Catalog],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(Catalog);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    // The app page's Public address panel (S290) reads its own status; app-public.spec covers it.
    http.match('/api/v1/catalog/jellyfin/public');
    http.match('/api/v1/catalog/jellyfin/move');
    // Bundle grading (S344) is advisory; bundles.spec-style tests flush it explicitly.
    http.match('/api/v1/catalog/bundles');
    http.verify();
  });

  function text(): string {
    return (fixture.nativeElement as HTMLElement).textContent!.replace(/\s+/g, ' ');
  }

  function load(): void {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    fixture.detectChanges();
  }

  it('grades the curated bundles against the appliance (S344)', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/catalog/bundles').flush([
      {
        Bundle: 'starter',
        Title: 'Starter',
        Blurb: 'Files, media and passwords on a modest appliance.',
        Members: [{ Name: 'jellyfin', Available: true, MemoryMB: 2048 }],
        Missing: ['vaultwarden'],
        Complete: false,
        TotalMemory: 2048,
        HostMemory: 16384,
        Storage: [{ Class: 'application', Needed: 1000, Free: 500, Known: true, OK: false }],
        Passed: false,
        Detail: 'bundle blocked: application storage needs 1000 bytes, host has 500',
      },
    ]);
    fixture.detectChanges();
    expect(text()).toContain('Starter');
    expect(text()).toContain('bundle blocked');
    expect(text()).toContain('Not in the catalog yet: vaultwarden');
  });

  it('lists curated packages', () => {
    load();
    expect(text()).toContain('Jellyfin');
    expect(text()).toContain('Media server you control.');
  });

  it('starts the install wizard from a package that is not installed, with no inline install form', () => {
    load();
    const link = (fixture.nativeElement as HTMLElement).querySelector('a[href="/admin/catalog/jellyfin/install"]');
    expect(link?.textContent?.trim()).toBe('Install Jellyfin');
    expect((fixture.nativeElement as HTMLElement).querySelector('input[name="domain"]')).toBeNull();
    expect(text()).not.toContain('Review plan');
  });

  it('offers Manage and Uninstall, not Install, for an installed package', () => {
    load();
    expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/admin/catalog/gridcraft/install"]')).toBeNull();
    expect(text()).toContain('Installed');
    expect(text()).toContain('Manage GridCraft');
    expect(text()).toContain('Uninstall GridCraft');
  });

  it('opens the Manage panel inside the app\'s own card and closes it again', () => {
    load();
    const root = fixture.nativeElement as HTMLElement;
    const button = Array.from(root.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Manage GridCraft')!;
    expect(button.getAttribute('aria-expanded')).toBe('false');
    button.click();
    http.expectOne('/api/v1/catalog/gridcraft').flush({ ...MANIFEST, Name: 'gridcraft', Title: 'GridCraft' });
    http.expectOne('/api/v1/edge/routes').flush({ routes: [] });
    http.expectOne('/api/v1/names').flush({ resolver: { enabled: false, running: false, listening: [], names: [], router_hint: '' }, apps: [] });
    fixture.detectChanges();
    http.match('/api/v1/catalog/gridcraft/public');
    http.match('/api/v1/catalog/gridcraft/move');
    const panel = root.querySelector('#manage-gridcraft');
    expect(panel?.closest('li.catalog-item')?.textContent).toContain('Uninstall GridCraft');
    expect(button.getAttribute('aria-expanded')).toBe('true');
    expect(root.querySelectorAll('.catalog-manage').length).toBe(1);
    button.click();
    fixture.detectChanges();
    expect(root.querySelector('#manage-gridcraft')).toBeNull();
    expect(button.getAttribute('aria-expanded')).toBe('false');
  });

  it('marks an app whose setup stopped and shows the setup in its card (S324)', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush([PACKAGES[0], { ...PACKAGES[1], setup: 'stopped' }]);
    fixture.detectChanges();
    expect(text()).toContain('Setup stopped');
    http.expectOne('/api/v1/catalog/gridcraft/setup').flush({
      app: 'gridcraft',
      status: 'stopped',
      reason: 'Stopped at the public address: the plan changed after it was approved',
      steps: [
        { name: 'address', state: 'done', digest: 'a' },
        { name: 'publish', state: 'changed', digest: 'p' },
      ],
    });
    fixture.detectChanges();
    expect(text()).toContain("GridCraft's setup stopped");
    expect(text()).toContain('Public address Plan changed');
    const review = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('button')).find((b) => b.textContent!.trim() === 'Review');
    expect(review).toBeDefined();
  });

  it('points an install that is still being set up back to its wizard', () => {
    TestBed.inject(InstallDrafts).save('gridcraft', { installOperation: 7 } as never);
    load();
    const link = (fixture.nativeElement as HTMLElement).querySelector('a[href="/admin/catalog/gridcraft/install"]');
    expect(link?.textContent?.trim()).toBe('Continue setting up GridCraft');
    expect(text()).toContain('Setting up');
    expect(text()).not.toContain('Installed');
  });

  it('invites a first step when the catalog is empty', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('The catalog is empty.');
  });

  it('says how to fix an unreachable appliance', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').error(new ProgressEvent('error'), { status: 0 });
    fixture.detectChanges();
    expect(text()).toContain('Cannot reach the appliance. Check that homestead-server is running');
  });

  it('applies configuration variables to an installed package', () => {
    load();
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin').flush(MANIFEST);
    http.expectOne('/api/v1/edge/routes').flush({
      routes: [{ Host: 'jellyfin.lan', Target: { Host: MANIFEST.Services[0].Name, Port: 8096 }, Exposure: 'local', TLS: 'internal' }],
    });
    http.expectOne('/api/v1/names').flush({
      resolver: { enabled: false, running: false, listening: [], names: [], router_hint: '' },
      apps: [{ host: 'jellyfin.lan', names: ['jellyfin.lan', 'jellyfin.local'] }],
    });
    fixture.detectChanges();
    expect(text()).toContain('Manage Jellyfin');
    expect(text()).toContain('answers at jellyfin.lan, jellyfin.local.');
    fixture.componentInstance['applyConfig']();
    http.expectOne('/api/v1/catalog/jellyfin/configure').flush({ id: 8 });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/admin/operations/8"]')).not.toBeNull();
  });

  it('registers and rotates secret-backed variables without showing values', () => {
    load();
    fixture.componentInstance['select']('jellyfin');
    http
      .expectOne('/api/v1/catalog/jellyfin')
      .flush({ ...MANIFEST, Secrets: [{ Name: 'db_password', Immutable: false }] });
    http.expectOne('/api/v1/edge/routes').flush({ routes: [] });
    http.expectOne('/api/v1/names').flush({ resolver: { enabled: false, running: false, listening: [], names: [], router_hint: '' }, apps: [] });
    const listed = {
      name: 'db_password',
      store_name: 'app_jellyfin_db_password',
      present: false,
      immutable: false,
      services: ['db'],
      database_password: true,
      wired: false,
      installed: true,
    };
    http.expectOne('/api/v1/catalog/jellyfin/secrets').flush([listed]);
    fixture.detectChanges();
    expect(text()).toContain('db_password');
    expect(text()).toContain('missing');

    fixture.componentInstance['patchSecretValue']('db_password', 'old-pass');
    fixture.componentInstance['registerSecret']('db_password');
    const register = http.expectOne('/api/v1/catalog/jellyfin/secrets/db_password');
    expect(register.request.body).toEqual({ value: 'old-pass', skip_verify: false, force: false });
    register.flush({ store_name: 'app_jellyfin_db_password' });
    http
      .expectOne('/api/v1/catalog/jellyfin/secrets')
      .flush([{ ...listed, present: true }]);
    fixture.detectChanges();
    expect(text()).toContain('present');
    expect(text()).not.toContain('old-pass');

    // Not wired: adoption is offered and rotation is not.
    expect(text()).toContain('Adopt secrets');
    fixture.componentInstance['adoptSecrets']();
    const adopt = http.expectOne('/api/v1/catalog/jellyfin/configure');
    expect(adopt.request.body).toEqual({ changes: [], adopt_secrets: true });
    adopt.flush({ id: 13 });

    fixture.componentInstance['rotateSecret']('db_password');
    const rotate = http.expectOne('/api/v1/catalog/jellyfin/configure');
    expect(rotate.request.body).toEqual({ changes: [], rotate_secrets: ['db_password'] });
    rotate.flush({ id: 12 });
  });

  it('reveals a credential meant for the owner after a confirmation (S312)', () => {
    load();
    fixture.componentInstance['select']('jellyfin');
    http
      .expectOne('/api/v1/catalog/jellyfin')
      .flush({ ...MANIFEST, Secrets: [{ Name: 'admin_password', Immutable: true }] });
    http.expectOne('/api/v1/edge/routes').flush({ routes: [] });
    http.expectOne('/api/v1/names').flush({ resolver: { enabled: false, running: false, listening: [], names: [], router_hint: '' }, apps: [] });
    http.expectOne('/api/v1/catalog/jellyfin/secrets').flush([
      {
        name: 'admin_password',
        store_name: 'app_jellyfin_admin_password',
        present: true,
        immutable: true,
        services: ['jellyfin'],
        database_password: false,
        wired: true,
        installed: true,
        reveal: true,
        note: 'Password for the administrator account "admin".',
      },
    ]);
    fixture.detectChanges();
    expect(text()).toContain('Password for the administrator account');
    const reveal = Array.from(fixture.nativeElement.querySelectorAll('button') as NodeListOf<HTMLButtonElement>).find(
      (b) => b.textContent?.trim() === 'Reveal',
    );
    expect(reveal).toBeDefined();
    reveal!.click();
    fixture.detectChanges();
    expect(text()).toContain('Show this password?');
    http.expectNone('/api/v1/catalog/jellyfin/secrets/admin_password/reveal');
    fixture.componentInstance['confirmReveal']();
    const req = http.expectOne('/api/v1/catalog/jellyfin/secrets/admin_password/reveal');
    expect(req.request.body).toEqual({ confirm: true });
    req.flush({ name: 'admin_password', value: 'shown-once-pw', note: '' });
    fixture.detectChanges();
    expect(text()).toContain('shown-once-pw');
    expect(text()).not.toContain('Show this password?');
    fixture.componentInstance['hideRevealed']();
    fixture.detectChanges();
    expect(text()).not.toContain('shown-once-pw');
  });
});
