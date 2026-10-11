import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Backups } from './backups';

const SCHEDULE = {
  Package: 'jellyfin',
  IntervalSeconds: 86400,
  Retain: 7,
  Enabled: true,
  Actor: 'chris',
  UpdatedAt: '2026-01-01T00:00:00Z',
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

  function load(schedules: unknown[], destination = '/mnt/backup') {
    fixture.detectChanges();
    http.expectOne('/api/v1/backups/destination').flush({ destination });
    http.expectOne('/api/v1/backups/schedules').flush(schedules);
    fixture.detectChanges();
  }

  it('shows destination and schedules', () => {
    load([SCHEDULE]);
    expect(text()).toContain('/mnt/backup');
    expect(text()).toContain('jellyfin');
    expect(text()).toContain('every 24h');
  });

  it('shows empty state and destination errors', () => {
    fixture.detectChanges();
    http
      .expectOne('/api/v1/backups/destination')
      .flush('no backup storage assigned', { status: 422, statusText: 'x' });
    http.expectOne('/api/v1/backups/schedules').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('No backup schedules yet');
    expect(text()).toContain('no backup storage assigned');
  });

  it('saves a schedule with hours converted to seconds', () => {
    load([]);
    fixture.componentInstance.save('jellyfin', '2', '', true, 'chris');
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
    http.expectOne('/api/v1/backups/destination').flush({ destination: '/b' });
    http.expectOne('/api/v1/backups/schedules').flush([SCHEDULE]);
    fixture.detectChanges();
    expect(text()).toContain('Saved schedule for jellyfin');
  });

  it('rejects invalid input without calling the API', () => {
    load([]);
    fixture.componentInstance.save('jellyfin', '0', '7', true, 'chris');
    fixture.detectChanges();
    expect(text()).toContain('at least 1');
    fixture.componentInstance.save('jellyfin', '1', '7', true, '');
    fixture.detectChanges();
    expect(text()).toContain('named actor');
  });

  it('inspects and removes a schedule', () => {
    load([SCHEDULE]);
    fixture.componentInstance.inspect('jellyfin');
    http.expectOne('/api/v1/backups/schedules/jellyfin').flush(SCHEDULE);
    fixture.detectChanges();
    expect(text()).toContain('Schedule detail: jellyfin');
    fixture.componentInstance.remove('jellyfin');
    const del = http.expectOne('/api/v1/backups/schedules/jellyfin');
    expect(del.request.method).toBe('DELETE');
    del.flush(null, { status: 204, statusText: 'No Content' });
    http.expectOne('/api/v1/backups/destination').flush({ destination: '/b' });
    http.expectOne('/api/v1/backups/schedules').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('Removed schedule for jellyfin');
    expect(text()).not.toContain('Schedule detail');
  });
});
