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

  it('lists curated packages', () => {
    load();
    expect(text()).toContain('Jellyfin');
    expect(text()).toContain('Media server you control.');
  });

  it('starts the install wizard from a package that is not installed, with no inline install form', () => {
    load();
    const link = (fixture.nativeElement as HTMLElement).querySelector('a[href="/catalog/jellyfin/install"]');
    expect(link?.textContent?.trim()).toBe('Install Jellyfin');
    expect((fixture.nativeElement as HTMLElement).querySelector('input[name="domain"]')).toBeNull();
    expect(text()).not.toContain('Review plan');
  });

  it('offers Manage and Uninstall, not Install, for an installed package', () => {
    load();
    expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/catalog/gridcraft/install"]')).toBeNull();
    expect(text()).toContain('Installed');
    expect(text()).toContain('Manage GridCraft');
    expect(text()).toContain('Uninstall GridCraft');
  });

  it('points an install that is still being set up back to its wizard', () => {
    TestBed.inject(InstallDrafts).save('gridcraft', { installOperation: 7 } as never);
    load();
    const link = (fixture.nativeElement as HTMLElement).querySelector('a[href="/catalog/gridcraft/install"]');
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
    fixture.detectChanges();
    expect(text()).toContain('Manage Jellyfin');
    fixture.componentInstance['applyConfig']();
    http.expectOne('/api/v1/catalog/jellyfin/configure').flush({ id: 8 });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/operations/8"]')).not.toBeNull();
  });

  it('registers and rotates secret-backed variables without showing values', () => {
    load();
    fixture.componentInstance['select']('jellyfin');
    http
      .expectOne('/api/v1/catalog/jellyfin')
      .flush({ ...MANIFEST, Secrets: [{ Name: 'db_password', Immutable: false }] });
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
});
