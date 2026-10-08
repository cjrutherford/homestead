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
    fixture.componentInstance['install']();
    http.expectOne('/api/v1/catalog/jellyfin/install').flush({ id: 7 });
    fixture.detectChanges();
    expect(text()).toContain('#7');
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
});
