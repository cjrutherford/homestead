import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { DataMigration } from './data-migration';

const STATUS = {
  apps: [
    {
      package: 'nextcloud',
      state: 'needs-migration',
      findings: [
        {
          service: 'nextcloud',
          target: '/var/www/html',
          state: 'needs-migration',
          kind: 'volume',
          volume: 'abc',
        },
      ],
    },
    { package: 'jellyfin', state: 'placed', findings: [] },
  ],
  old_volumes: [
    {
      volume: 'old1',
      package: 'jellyfin',
      service: 'jellyfin',
      target: '/config',
      size_bytes: 5,
      state: 'pending-removal',
    },
  ],
};

const PLAN = {
  package: 'nextcloud',
  digest: 'd1',
  items: [
    {
      service: 'nextcloud',
      target: '/var/www/html',
      source: '/v/_data',
      dest: '/mnt/nextcloud/html',
      size_bytes: 9,
    },
  ],
  old_volumes: [{ name: 'abc', size_bytes: 9 }],
  space: [{ mountpoint: '/mnt', needed_bytes: 9, free_bytes: 100, ok: true }],
  estimated_downtime_seconds: 42,
  notes: ['old volumes are kept'],
  backup: { possible: false, reason: 'no backup destination' },
};

describe('DataMigration', () => {
  let fixture: ComponentFixture<DataMigration>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [DataMigration],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(DataMigration);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  function buttons(label: string): HTMLButtonElement[] {
    return Array.from(
      (fixture.nativeElement as HTMLElement).querySelectorAll('button'),
    ).filter((button) => button.textContent?.includes(label));
  }

  function load(): void {
    fixture.detectChanges();
    http.expectOne('/api/v1/data-migrations').flush(STATUS);
    fixture.detectChanges();
  }

  it('shows placement states and pending old volumes', () => {
    load();
    expect(text()).toContain('nextcloud: needs-migration');
    expect(text()).toContain('jellyfin: placed');
    expect(text()).toContain('old1');
    expect(buttons('Review migration plan').length).toBe(1);
  });

  it('requires acknowledging a missing backup before approving the quoted digest', () => {
    load();
    buttons('Review migration plan')[0].click();
    http.expectOne('/api/v1/data-migrations/nextcloud/plan').flush(PLAN);
    fixture.detectChanges();
    expect(text()).toContain('/mnt/nextcloud/html');
    expect(text()).toContain('about 42 seconds');
    expect(text()).toContain('no backup destination');
    expect(buttons('Approve migration')[0].disabled).toBe(true);

    const box = (fixture.nativeElement as HTMLElement).querySelector(
      'input[name="acknowledge-no-backup"]',
    ) as HTMLInputElement;
    box.click();
    fixture.detectChanges();
    buttons('Approve migration')[0].click();
    const request = http.expectOne('/api/v1/data-migrations/nextcloud/approve');
    expect(request.request.body).toEqual({
      digest: 'd1',
      acknowledge_no_backup: true,
    });
    request.flush({ id: 9 });
    fixture.detectChanges();
    expect(text()).toContain('operation 9');
  });

  it('removes old volumes only on the explicit action', () => {
    load();
    buttons('Remove old volumes')[0].click();
    const request = http.expectOne('/api/v1/data-migrations/jellyfin/remove-volumes');
    expect(request.request.body).toEqual({ volumes: ['old1'] });
    request.flush({ id: 4 });
  });

  it('surfaces a plan refusal', () => {
    load();
    buttons('Review migration plan')[0].click();
    http
      .expectOne('/api/v1/data-migrations/nextcloud/plan')
      .flush('not enough free space', { status: 422, statusText: 'Unprocessable' });
    fixture.detectChanges();
    expect(text()).toContain('not enough free space');
  });
});
