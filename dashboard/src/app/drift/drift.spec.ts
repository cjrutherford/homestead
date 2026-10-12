import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Drift } from './drift';

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

const REVIEW = [
  {
    path: 'compose.yaml',
    kind: 'modified',
    recorded: 'services: {managed: true}\n',
    current: 'services: {expert: true}\n',
  },
];

const SCHEDULE = {
  interval_seconds: 86400,
  enabled: true,
  updated_at: '2026-10-10T12:00:00Z',
  last_scan: {
    scanned_at: '2026-10-11T12:00:00Z',
    scanned: 2,
    drifted: [
      { package: 'jellyfin', files: [{ path: 'compose.yaml', kind: 'modified' }] },
    ],
    errors: [],
  },
};

describe('Drift', () => {
  let fixture: ComponentFixture<Drift>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Drift],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Drift);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('reviews both sides of drifted files', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush(SCHEDULE);
    fixture.detectChanges();
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin/drift').flush(REVIEW);
    fixture.detectChanges();
    expect(text()).toContain('services: {managed: true}');
    expect(text()).toContain('services: {expert: true}');
    expect(text()).toContain('modified');
  });

  it('accepts current files and refreshes', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush(SCHEDULE);
    fixture.detectChanges();
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin/drift').flush(REVIEW);
    fixture.detectChanges();
    fixture.componentInstance['accept']();
    const accept = http.expectOne('/api/v1/catalog/jellyfin/drift');
    expect(accept.request.body).toMatchObject({ action: 'accept' });
    accept.flush({ accepted: ['compose.yaml'] });
    const refresh = http.expectOne('/api/v1/catalog/jellyfin/drift');
    refresh.flush([]);
    fixture.detectChanges();
    expect(text()).toContain('Adopted 1 file(s)');
    expect(text()).toContain('No drift');
  });

  it('restores through operations', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush(SCHEDULE);
    fixture.detectChanges();
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin/drift').flush(REVIEW);
    fixture.detectChanges();
    fixture.componentInstance['restore']();
    const restore = http.expectOne('/api/v1/catalog/jellyfin/drift');
    expect(restore.request.body).toMatchObject({ action: 'restore' });
    restore.flush({ id: 11 });
    fixture.detectChanges();
    expect(text()).toContain('Follow operation 11');
  });

  it('shows the last scheduled scan and saves the cadence', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush(SCHEDULE);
    fixture.detectChanges();
    expect(text()).toContain('Scans every 24 hour(s)');
    expect(text()).toContain('2026-10-11T12:00:00Z');
    expect(text()).toContain('compose.yaml (modified)');
    fixture.componentInstance['saveSchedule']('6', false);
    const save = http.expectOne('/api/v1/drift/schedule');
    expect(save.request.method).toBe('POST');
    expect(save.request.body).toEqual({ interval_seconds: 21600, enabled: false });
    save.flush({ ...SCHEDULE, interval_seconds: 21600, enabled: false });
    fixture.detectChanges();
    expect(text()).toContain('Scans every 6 hour(s)');
    expect(text()).toContain('off');
  });
});
