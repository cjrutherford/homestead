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

const LIVE_CLEAN = { checked: true, findings: [] };

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

  function restoreButtons(): number {
    const buttons = Array.from(
      (fixture.nativeElement as HTMLElement).querySelectorAll('button'),
    );
    return buttons.filter((b) => b.textContent?.includes('Restore managed state')).length;
  }

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('reviews both sides of drifted files', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush(SCHEDULE);
    fixture.detectChanges();
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin/drift/live').flush(LIVE_CLEAN);
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
    http.expectOne('/api/v1/catalog/jellyfin/drift/live').flush(LIVE_CLEAN);
    http.expectOne('/api/v1/catalog/jellyfin/drift').flush(REVIEW);
    fixture.detectChanges();
    fixture.componentInstance['accept']();
    const accept = http.expectOne('/api/v1/catalog/jellyfin/drift');
    expect(accept.request.body).toMatchObject({ action: 'accept' });
    accept.flush({ accepted: ['compose.yaml'] });
    http.expectOne('/api/v1/catalog/jellyfin/drift/live').flush(LIVE_CLEAN);
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
    http.expectOne('/api/v1/catalog/jellyfin/drift/live').flush(LIVE_CLEAN);
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
    fixture.componentInstance['saveSchedule']('360', false);
    const save = http.expectOne('/api/v1/drift/schedule');
    expect(save.request.method).toBe('POST');
    expect(save.request.body).toEqual({ interval_seconds: 21600, enabled: false });
    save.flush({ ...SCHEDULE, interval_seconds: 21600, enabled: false });
    fixture.detectChanges();
    expect(text()).toContain('Scans every 6 hour(s)');
    expect(text()).toContain('off');
  });

  it('round-trips an unchanged interval exactly, including the 5 minute minimum', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush(SCHEDULE);
    for (const seconds of [300, 330, 420, 3600, 5400, 86400]) {
      fixture.componentInstance['schedule'].set({ ...SCHEDULE, interval_seconds: seconds });
      fixture.detectChanges();
      const input = fixture.nativeElement.querySelector('input[type="number"]') as HTMLInputElement;
      expect(input.min).toBe('5');
      fixture.componentInstance['saveSchedule'](input.value, true);
      const save = http.expectOne('/api/v1/drift/schedule');
      expect(save.request.body).toEqual({ interval_seconds: seconds, enabled: true });
      save.flush({ ...SCHEDULE, interval_seconds: seconds });
    }
  });

  it('labels 300 seconds exactly', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush({ ...SCHEDULE, interval_seconds: 300 });
    fixture.detectChanges();
    expect(text()).toContain('Scans every 5 minute(s)');
  });

  it('shows running-container findings with their resolution path', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush({
      ...SCHEDULE,
      last_scan: {
        ...SCHEDULE.last_scan,
        drifted: [
          {
            package: 'jellyfin',
            files: [],
            live: [
              { service: 'web', kind: 'missing', detail: 'no container exists for this service', resolution: 'restore' },
            ],
          },
        ],
        live_skipped: 'Docker engine unreachable',
      },
    });
    fixture.detectChanges();
    expect(text()).toContain('live missing web');
    expect(text()).toContain('Live comparison skipped: Docker engine unreachable');
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin/drift/live').flush({
      checked: true,
      findings: [
        { service: 'web', container: 'jellyfin-web', kind: 'port', detail: 'host port 8096 -> 8096/tcp is not published', resolution: 'recreate', hint: 'docker compose -p jellyfin rm -sf web, then Restore managed state' },
        { service: 'web', kind: 'extra', detail: 'stray container', resolution: 'manual', hint: 'if unwanted: docker rm -f stray' },
      ],
    });
    http.expectOne('/api/v1/catalog/jellyfin/drift').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('jellyfin-web: host port 8096 -> 8096/tcp is not published');
    expect(text()).toContain('resolve manually');
    expect(text()).toContain('docker compose -p jellyfin rm -sf web');
    expect(text()).toContain('docker rm -f stray');
    // Recreate and manual findings cannot be fixed by Restore alone.
    expect(restoreButtons()).toBe(0);
  });

  it('offers Restore only when a finding restore can fix exists', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush(SCHEDULE);
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin/drift/live').flush({
      checked: true,
      findings: [
        { service: 'web', kind: 'missing', detail: 'no container exists for this service', resolution: 'restore' },
      ],
    });
    http.expectOne('/api/v1/catalog/jellyfin/drift').flush([]);
    fixture.detectChanges();
    expect(restoreButtons()).toBe(1);
  });

  it('shows an owner-stopped app as a state, with no Restore', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush({
      ...SCHEDULE,
      last_scan: { ...SCHEDULE.last_scan, drifted: [], stopped: ['jellyfin'], live_notes: ['jellyfin: service web image uses interpolation; image comparison skipped'] },
    });
    fixture.detectChanges();
    expect(text()).toContain('jellyfin is stopped (a run state, not drift)');
    expect(text()).toContain('Live comparison limit: jellyfin: service web image');
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin/drift/live').flush({
      checked: true, findings: [], stack_state: 'stopped', unsupported: ['service web image uses interpolation'],
    });
    http.expectOne('/api/v1/catalog/jellyfin/drift').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('no service in this app is running');
    expect(text()).toContain('Not compared: service web image uses interpolation');
    expect(restoreButtons()).toBe(0);
  });

  it('says so when Docker was unreachable', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog').flush(PACKAGES);
    http.expectOne('/api/v1/drift/schedule').flush(SCHEDULE);
    fixture.componentInstance['select']('jellyfin');
    http.expectOne('/api/v1/catalog/jellyfin/drift/live').flush({ checked: false, findings: [] });
    http.expectOne('/api/v1/catalog/jellyfin/drift').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('Docker engine unreachable');
    expect(text()).not.toContain('Restore managed state');
  });
});
