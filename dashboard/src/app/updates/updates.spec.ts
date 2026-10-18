import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Updates } from './updates';

const ROWS = [
  {
    package: 'jellyfin',
    installed: true,
    plan: {
      Package: 'jellyfin',
      Services: [
        {
          Service: 'jellyfin',
          Current: 'jellyfin/jellyfin:12.1',
          Target: 'jellyfin/jellyfin:12.2',
          Source: 'manifest',
        },
      ],
      Backup: true,
      BackupWhy: 'backup paths declared',
      Affected: ['jellyfin'],
      Warnings: [],
    },
  },
  {
    package: 'plain',
    installed: false,
    plan: {
      Package: 'plain',
      Services: [],
      Backup: false,
      BackupWhy: '',
      Affected: [],
      Warnings: [],
    },
  },
];

describe('Updates', () => {
  let fixture: ComponentFixture<Updates>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Updates],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Updates);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    // The embedded platform panel (S264) loads its own data; its
    // behavior is covered in platform-updates.spec.ts.
    // The craft images panel (S279) is covered in craft-images.spec.ts.
    for (const request of http.match(
      (r) =>
        r.url.startsWith('/api/v1/platform/') ||
        r.url.startsWith('/api/v1/craft'),
    )) {
      request.flush([]);
    }
    http.verify();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('reviews per-package drift plans', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/updates').flush(ROWS);
    fixture.detectChanges();
    expect(text()).toContain('jellyfin/jellyfin:12.1');
    expect(text()).toContain('jellyfin/jellyfin:12.2');
    expect(text()).toContain('Not installed');
  });

  it('requires backup opt-in before applying guarded updates', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/updates').flush(ROWS);
    fixture.detectChanges();
    const buttons = [
      ...fixture.nativeElement.querySelectorAll('button'),
    ] as HTMLButtonElement[];
    const guarded = buttons.find((button) =>
      button.textContent?.includes('Apply update'),
    );
    expect(guarded?.disabled).toBe(true);
    fixture.componentInstance['setBackup']('jellyfin', true);
    fixture.detectChanges();
    fixture.componentInstance['apply']('jellyfin');
    const apply = http.expectOne('/api/v1/updates/jellyfin/apply');
    expect(apply.request.body).toMatchObject({ backup: true });
    apply.flush({ id: 9 });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/operations/9"]')).not.toBeNull();
  });

  it('shows the skipped-backup note of a stateless package with its operation', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/updates').flush(ROWS);
    fixture.detectChanges();
    fixture.componentInstance['setBackup']('jellyfin', true);
    fixture.componentInstance['apply']('jellyfin');
    http
      .expectOne('/api/v1/updates/jellyfin/apply')
      .flush({ id: 11, detail: 'backup skipped: jellyfin is stateless' });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/operations/11"]')).not.toBeNull();
    expect(text()).toContain('backup skipped: jellyfin is stateless');
  });

  it('reports no-op updates without opening operations', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/updates').flush(ROWS);
    fixture.detectChanges();
    fixture.componentInstance['setBackup']('jellyfin', true);
    fixture.componentInstance['apply']('jellyfin');
    http
      .expectOne('/api/v1/updates/jellyfin/apply')
      .flush({ id: 0, detail: 'already current' });
    const refresh = http.expectOne('/api/v1/updates');
    refresh.flush(ROWS);
    fixture.detectChanges();
    expect(text()).toContain('already current');
  });
});

describe('Updates craft inputs (S279)', () => {
  it('sends craft inputs with the apply only when the package has craft moves', async () => {
    await TestBed.configureTestingModule({
      imports: [Updates],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    const fixture = TestBed.createComponent(Updates);
    const http = TestBed.inject(HttpTestingController);
    const page = fixture.componentInstance as any;
    page.setCraft('wordcraft', 'manifest', '/srv/m.yaml');
    page.setCraftOlder('wordcraft', true);
    page.apply('wordcraft');
    const request = http.expectOne('/api/v1/updates/wordcraft/apply');
    expect(request.request.body.craft).toEqual({
      manifest: '/srv/m.yaml',
      allow_older: true,
    });
    request.flush({ id: 3 });
    page.apply('jellyfin');
    const plain = http.expectOne('/api/v1/updates/jellyfin/apply');
    expect(plain.request.body.craft).toBeUndefined();
    plain.flush({ id: 4 });
    for (const r of http.match((x) => x.url.startsWith('/api/v1/craft') || x.url.startsWith('/api/v1/platform/') || x.url === '/api/v1/updates')) {
      r.flush([]);
    }
  });
});
