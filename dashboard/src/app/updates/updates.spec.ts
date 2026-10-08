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
    expect(text()).toContain('Follow operation 9');
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
