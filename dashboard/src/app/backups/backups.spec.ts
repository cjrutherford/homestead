import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Backups } from './backups';

const SCHEDULE = {
  package: 'jellyfin',
  interval_seconds: 86400,
  retain: 7,
  enabled: true,
  actor: 'chris',
  updated_at: '2026-01-01T00:00:00Z',
};

const JOB = {
  id: 'backup:jellyfin',
  interval_seconds: 86400,
  enabled: true,
  last_run_at: '2026-01-02T00:00:00Z',
  last_operation_id: 4,
  last_error: '',
  last_skip_at: '0001-01-01T00:00:00Z',
  last_skip_due: '0001-01-01T00:00:00Z',
  last_skip_blocking: 0,
  next_due: '2026-01-03T00:00:00Z',
  running: false,
};

describe('Backups', () => {
  let fixture: ComponentFixture<Backups>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Backups],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(Backups);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  const text = () => fixture.nativeElement.textContent as string;

  function load(
    schedules: unknown[],
    destination = '/mnt/backup',
    jobs: unknown[] = [],
  ) {
    fixture.detectChanges();
    http
      .expectOne('/api/v1/backups/destination')
      .flush({ destination, encrypted: true, format_version: 2 });
    http.expectOne('/api/v1/scheduler/jobs').flush(jobs);
    http.expectOne('/api/v1/backups/schedules').flush(schedules);
    fixture.detectChanges();
  }

  function reload(schedules: unknown[], jobs: unknown[] = []) {
    http.expectOne('/api/v1/backups/destination').flush({ destination: '/b' });
    http.expectOne('/api/v1/scheduler/jobs').flush(jobs);
    http.expectOne('/api/v1/backups/schedules').flush(schedules);
    fixture.detectChanges();
  }

  function save(
    name: string,
    hours: string,
    retain: string,
    actor: string,
  ) {
    fixture.componentInstance.save(name, hours, retain, true, actor);
    fixture.detectChanges();
  }

  it('shows destination and schedules', () => {
    load([SCHEDULE]);
    expect(text()).toContain('/mnt/backup');
    expect(text()).toContain('Encryption: age, format 2');
    expect(text()).toContain('jellyfin');
    expect(text()).toContain('every 24h');
    expect(fixture.nativeElement.querySelector('code.backup-root')).toBeTruthy();
  });

  it('shows non-integer hours sensibly and unlimited retention', () => {
    load([{ ...SCHEDULE, interval_seconds: 5400, retain: 0 }]);
    expect(text()).toContain('every 1.5h');
    expect(text()).toContain('retain all runs');
  });

  it('shows empty state and destination errors', () => {
    fixture.detectChanges();
    http
      .expectOne('/api/v1/backups/destination')
      .flush('no backup storage assigned', { status: 422, statusText: 'x' });
    http.expectOne('/api/v1/scheduler/jobs').flush([]);
    http.expectOne('/api/v1/backups/schedules').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('No backup schedules yet');
    expect(text()).toContain('no backup storage assigned');
  });

  it('saves a schedule with hours converted to seconds and trimmed text', () => {
    load([]);
    save(' jellyfin ', ' 2 ', '', ' chris ');
    const req = http.expectOne('/api/v1/backups/schedules');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({
      package: 'jellyfin',
      interval_seconds: 7200,
      retain: 7,
      enabled: true,
      actor: 'chris',
    });
    req.flush(SCHEDULE);
    reload([SCHEDULE]);
    expect(text()).toContain('Saved schedule for jellyfin');
  });

  it('accepts retain 0 as keep everything', () => {
    load([]);
    save('jellyfin', '1', '0', 'chris');
    const req = http.expectOne('/api/v1/backups/schedules');
    expect(req.request.body.retain).toBe(0);
    req.flush(SCHEDULE);
    reload([SCHEDULE]);
  });

  it('rejects invalid input without calling the API', () => {
    load([]);
    save('jellyfin', '0', '7', 'chris');
    expect(text()).toContain('at least 1');
    save('jellyfin', '1.5', '7', 'chris');
    expect(text()).toContain('whole number of hours');
    save('jellyfin', '1e2', '7', 'chris');
    expect(text()).toContain('whole number of hours');
    save('jellyfin', ' ', '7', 'chris');
    expect(text()).toContain('whole number of hours');
    save('jellyfin', '1', '2.5', 'chris');
    expect(text()).toContain('Retain must be a whole number');
    save('jellyfin', '1', '-1', 'chris');
    expect(text()).toContain('Retain must be a whole number');
    save('jellyfin', '1', ' 3x ', 'chris');
    expect(text()).toContain('Retain must be a whole number');
    save('jellyfin', '1', '7', '');
    expect(text()).toContain('named actor');
    save('jellyfin', '1', '7', '   ');
    expect(text()).toContain('named actor');
  });

  it('surfaces a server rejection on save', () => {
    load([]);
    save('jellyfin', '1', '7', 'chris');
    http
      .expectOne('/api/v1/backups/schedules')
      .flush('backup: invalid schedule: nope', {
        status: 422,
        statusText: 'Unprocessable',
      });
    fixture.detectChanges();
    expect(text()).toContain('invalid schedule');
  });

  it('inspects a schedule', () => {
    load([SCHEDULE]);
    fixture.componentInstance.inspect('jellyfin');
    http.expectOne('/api/v1/backups/schedules/jellyfin').flush(SCHEDULE);
    fixture.detectChanges();
    expect(text()).toContain('Schedule detail: jellyfin');
  });

  it('reports a failed inspect', () => {
    load([SCHEDULE]);
    fixture.componentInstance.inspect('jellyfin');
    http
      .expectOne('/api/v1/backups/schedules/jellyfin')
      .flush('backup: schedule not found', { status: 404, statusText: 'x' });
    fixture.detectChanges();
    expect(text()).toContain('schedule not found');
    expect(text()).not.toContain('Schedule detail');
  });

  it('asks for confirmation before removing', () => {
    load([SCHEDULE]);
    fixture.componentInstance.inspect('jellyfin');
    http.expectOne('/api/v1/backups/schedules/jellyfin').flush(SCHEDULE);
    fixture.componentInstance.askRemove('jellyfin');
    fixture.detectChanges();
    http.expectNone('/api/v1/backups/schedules/jellyfin');
    expect(text()).toContain('Remove the schedule for jellyfin?');
    fixture.componentInstance.cancelRemove();
    fixture.detectChanges();
    expect(text()).not.toContain('Remove the schedule for jellyfin?');

    fixture.componentInstance.askRemove('jellyfin');
    fixture.componentInstance.remove('jellyfin');
    const del = http.expectOne('/api/v1/backups/schedules/jellyfin');
    expect(del.request.method).toBe('DELETE');
    del.flush(null, { status: 204, statusText: 'No Content' });
    reload([]);
    expect(text()).toContain('Removed schedule for jellyfin');
    expect(text()).not.toContain('Schedule detail');
  });

  it('reports a failed removal', () => {
    load([SCHEDULE]);
    fixture.componentInstance.remove('jellyfin');
    http
      .expectOne('/api/v1/backups/schedules/jellyfin')
      .flush('boom', { status: 500, statusText: 'x' });
    fixture.detectChanges();
    expect(text()).toContain('boom');
    expect(text()).not.toContain('Removed schedule');
  });

  it('lists scheduled jobs read-only with last run and next due', () => {
    load([SCHEDULE], '/mnt/backup', [JOB]);
    expect(text()).toContain('Scheduled jobs');
    expect(text()).toContain('backup:jellyfin');
    expect(text()).toContain('last run 2026-01-02T00:00:00Z');
    expect(text()).toContain('next due 2026-01-03T00:00:00Z');
  });

  it('shows job errors, skips, and the empty and error states', () => {
    load([], '/mnt/backup', [
      {
        ...JOB,
        last_run_at: '0001-01-01T00:00:00Z',
        last_error: 'catalog unavailable',
        last_skip_at: '2026-01-02T00:00:00Z',
        last_skip_blocking: 9,
      },
    ]);
    expect(text()).toContain('last run never');
    expect(text()).toContain('Last error: catalog unavailable');
    expect(text()).toContain('operation 9 was still active');

    fixture.componentInstance.refresh();
    http.expectOne('/api/v1/backups/destination').flush({ destination: '/b' });
    http.expectOne('/api/v1/scheduler/jobs').flush([]);
    http.expectOne('/api/v1/backups/schedules').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('No scheduled jobs.');

    fixture.componentInstance.refresh();
    http.expectOne('/api/v1/backups/destination').flush({ destination: '/b' });
    http
      .expectOne('/api/v1/scheduler/jobs')
      .flush('scheduler down', { status: 500, statusText: 'x' });
    http.expectOne('/api/v1/backups/schedules').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('scheduler down');
  });
});
