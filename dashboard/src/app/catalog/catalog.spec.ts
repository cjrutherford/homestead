import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Catalog } from './catalog';

const PACKAGES = [
  {
    name: 'jellyfin',
    version: '12.2',
    title: 'Jellyfin',
    blurb: 'Media server you control.',
    category: 'media',
    icon: 'mdi:plex',
  },
];

const MANIFEST = {
  Name: 'jellyfin',
  Version: '12.2',
  Title: 'Jellyfin',
  Blurb: 'Media server you control.',
  Description: 'Streams media.',
  Category: 'media',
  Services: [{ name: 'jellyfin', image: 'jellyfin/jellyfin:12.2', exposes: 'web' }],
  Requirements: {
    MemoryMB: 2048,
    Storage: [{ Class: 'bulk', MinBytes: 1 }],
    Ports: [8096],
  },
  Exposure: { Package: 'jellyfin', Endpoints: [] },
  BackupPaths: ['config'],
};

const PLAN = {
  Package: 'jellyfin',
  Requirements: { Passed: true },
  Routes: [{ Host: 'jellyfin.lan', Exposure: 'local' }],
  Compose: { ComposeYAML: 'name: jellyfin' },
  DNS: [],
  TLS: [],
  Firewall: [],
  Directories: [],
  Mounts: [
    {
      Service: 'jellyfin',
      Target: '/config',
      Source: '/mnt/app/jellyfin/config',
      Class: 'application',
      Database: false,
      Ephemeral: false,
      ReadOnly: false,
      Declared: true,
    },
  ],
  Notes: [],
  Actions: ['provision', 'up'],
};

describe('Catalog', () => {
  let fixture: ComponentFixture<Catalog>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Catalog],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Catalog);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('lists curated packages', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    fixture.detectChanges();
    expect(text()).toContain('Jellyfin');
  });

  it('reviews a plan then installs through operations', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    fixture.detectChanges();
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin').flush(MANIFEST);
    fixture.detectChanges();
    expect(text()).toContain('Streams media.');
    fixture.componentInstance['reviewPlan']();
    const plan = http.expectOne('/api/v1/catalog/jellyfin/plan');
    plan.flush(PLAN);
    fixture.detectChanges();
    expect(text()).toContain('provision, up');
    expect(text()).toContain('/mnt/app/jellyfin/config');
    fixture.componentInstance['install']();
    http.expectOne('/api/v1/catalog/jellyfin/install').flush({ id: 7 });
    fixture.detectChanges();
    expect(text()).toContain('#7');
  });

  it('disables Install for installed packages', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush([{ ...PACKAGES[0], installed: true }]);
    fixture.detectChanges();
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin').flush(MANIFEST);
    fixture.componentInstance['reviewPlan']();
    http.expectOne('/api/v1/catalog/jellyfin/plan').flush(PLAN);
    fixture.detectChanges();
    expect(text()).toContain('Already installed');
    const button = Array.from(
      (fixture.nativeElement as HTMLElement).querySelectorAll('button'),
    ).find((b) => b.textContent?.trim() === 'Install') as HTMLButtonElement;
    expect(button.disabled).toBe(true);
  });

  it('applies configuration variables', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    fixture.detectChanges();
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin').flush(MANIFEST);
    fixture.detectChanges();
    fixture.componentInstance['applyConfig']();
    const configure = http.expectOne('/api/v1/catalog/jellyfin/configure');
    configure.flush({ id: 8 });
    fixture.detectChanges();
    expect(text()).toContain('#8');
  });

  it('registers and rotates secret-backed variables without showing values', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    fixture.detectChanges();
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
