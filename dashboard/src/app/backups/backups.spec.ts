import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { By } from '@angular/platform-browser';
import { provideRouter } from '@angular/router';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Backups } from './backups';
import { RecoveryIdentity } from './recovery-identity';

const SCHEDULE = {
  package: 'jellyfin',
  interval_seconds: 86400,
  retain: 7,
  enabled: true,
  actor: 'chris',
  updated_at: '2026-01-01T00:00:00Z',
};

const RECIPIENTS = {
  recipients: [
    {
      fingerprint: 'aa11',
      public_key: 'age1appliance',
      label: 'appliance identity',
      kind: 'appliance',
      actor: 'system',
    },
  ],
  owner_held: false,
  appliance_created: true,
  note: 'New runs only.',
};
const IDENTITY = {
  created: true,
  recipient: 'age1appliance',
  fingerprint: 'aa11',
  exported: false,
  acknowledged: false,
};

const POLICY = {
  proposed: { time_of_day: '02:00', retain: 7, enabled: true },
  covered: ['jellyfin'],
  skipped: [{ package: 'wordcraft', reason: 'Keeps no data on the appliance.' }],
  time_zone: 'UTC',
  backup_storage: true,
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
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
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
    http.expectOne('/api/v1/backups/recipients').flush(RECIPIENTS);
    http.expectOne('/api/v1/backups/identity').flush(IDENTITY);
    http.expectOne('/api/v1/backups/schedules').flush(schedules);
    http.expectOne('/api/v1/backups/policy').flush(POLICY);
    fixture.detectChanges();
  }

  function reload(schedules: unknown[], jobs: unknown[] = []) {
    http.expectOne('/api/v1/backups/destination').flush({ destination: '/b' });
    http.expectOne('/api/v1/scheduler/jobs').flush(jobs);
    http.expectOne('/api/v1/backups/recipients').flush(RECIPIENTS);
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
    expect(text()).toContain('Nightly backups');
    expect(text()).toContain('Covers: jellyfin.');
    expect(text()).toContain('/mnt/backup');
    expect(text()).toContain('Encryption: age, format 2');
    expect(text()).toContain('jellyfin');
    expect(text()).toContain('every 24h');
    expect(fixture.nativeElement.querySelector('code.backup-root')).toBeTruthy();
  });

  it('shows non-integer hours sensibly and unlimited retention', () => {
    load([{ ...SCHEDULE, interval_seconds: 5400, retain: 0 }]);
    expect(text()).toContain('every 1.5h');
    expect(text()).toContain('all runs');
  });

  it('shows empty state and a not-yet-assigned destination as a state', () => {
    fixture.detectChanges();
    http
      .expectOne('/api/v1/backups/destination')
      .flush('no backup storage assigned', { status: 422, statusText: 'x' });
    http.expectOne('/api/v1/scheduler/jobs').flush([]);
    http.expectOne('/api/v1/backups/recipients').flush(RECIPIENTS);
    http.expectOne('/api/v1/backups/identity').flush(IDENTITY);
    http.expectOne('/api/v1/backups/schedules').flush([]);
    http.expectOne('/api/v1/backups/policy').flush({ ...POLICY, backup_storage: false });
    fixture.detectChanges();
    expect(text()).toContain('No backup schedules yet');
    expect(text()).toContain('No backup destination yet.');
    expect(fixture.nativeElement.querySelector('a[href="/admin/storage"]')).not.toBeNull();
  });

  it('still shows a destination failure that is not a missing assignment', () => {
    fixture.detectChanges();
    http
      .expectOne('/api/v1/backups/destination')
      .flush('disk unreadable', { status: 500, statusText: 'x' });
    http.expectOne('/api/v1/scheduler/jobs').flush([]);
    http.expectOne('/api/v1/backups/recipients').flush(RECIPIENTS);
    http.expectOne('/api/v1/backups/identity').flush(IDENTITY);
    http.expectOne('/api/v1/backups/schedules').flush([]);
    http.expectOne('/api/v1/backups/policy').flush(POLICY);
    fixture.detectChanges();
    expect(text()).toContain('disk unreadable');
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
    expect(text()).toContain('name of the owner');
    save('jellyfin', '1', '7', '   ');
    expect(text()).toContain('name of the owner');
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

  it('shows a stateless package as a no-op', () => {
    const stateless = { ...SCHEDULE, package: 'wordcraft', stateless: true };
    load([stateless]);
    expect(text()).toContain('stateless (no-op');
    expect(text()).not.toContain('every 1 day');
    fixture.componentInstance.inspect('wordcraft');
    http.expectOne('/api/v1/backups/schedules/wordcraft').flush(stateless);
    fixture.detectChanges();
    expect(text()).toContain('Schedule detail: wordcraft');
    expect(text()).toContain('keeps nothing on the appliance');
    expect(text()).not.toContain('cadence window');
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
    expect(text()).toContain('2 Jan 2026, 00:00 UTC');
    expect(text()).toContain('3 Jan 2026, 00:00 UTC');
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
    expect(text()).toContain('never');
    expect(text()).toContain('Last error: catalog unavailable');
    expect(text()).toContain('operation 9 was still active');

    fixture.componentInstance.refresh();
    http.expectOne('/api/v1/backups/destination').flush({ destination: '/b' });
    http.expectOne('/api/v1/scheduler/jobs').flush([]);
    http.expectOne('/api/v1/backups/recipients').flush(RECIPIENTS);
    http.expectOne('/api/v1/backups/schedules').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('No scheduled jobs.');

    fixture.componentInstance.refresh();
    http.expectOne('/api/v1/backups/destination').flush({ destination: '/b' });
    http
      .expectOne('/api/v1/scheduler/jobs')
      .flush('scheduler down', { status: 500, statusText: 'x' });
    http.expectOne('/api/v1/backups/recipients').flush(RECIPIENTS);
    http.expectOne('/api/v1/backups/schedules').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('scheduler down');
  });

  it('warns while only the appliance recipient exists', () => {
    load([]);
    expect(text()).toContain('cannot be read');
    expect(text()).toContain('Appliance identity');
    expect(text()).toContain('age1appliance');
  });

  it('adds a recipient and rejects a malformed key locally', () => {
    load([]);
    fixture.componentInstance.addRecipient('ssh-ed25519 AAAA', '');
    fixture.detectChanges();
    expect(text()).toContain('starting with age1');
    fixture.componentInstance.addRecipient(' age1abc ', ' laptop ');
    const req = http.expectOne('/api/v1/backups/recipients');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ public_key: 'age1abc', label: 'laptop' });
    req.flush({ fingerprint: 'bb22', public_key: 'age1abc', kind: 'owner' });
    http.expectOne('/api/v1/backups/destination').flush({ destination: '/b' });
    http.expectOne('/api/v1/scheduler/jobs').flush([]);
    http.expectOne('/api/v1/backups/recipients').flush(RECIPIENTS);
    http.expectOne('/api/v1/backups/schedules').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('not re-encrypted');
  });

  const identityPanel = () =>
    fixture.debugElement.query(By.directive(RecoveryIdentity)).componentInstance as RecoveryIdentity & {
      exported(): unknown;
    };
  const EXPORT = {
    identity: 'AGE-SECRET-KEY-TEST',
    recipient: 'age1appliance',
    fingerprint: 'aa11',
    exported_at: 'now',
    actor: 'chris',
  };

  it('exports the identity only after confirmation and shows it once', () => {
    load([]);
    identityPanel().askExport();
    fixture.detectChanges();
    expect(text()).toContain('audited');
    http.expectNone('/api/v1/backups/identity/export');
    identityPanel().confirmExport();
    const req = http.expectOne('/api/v1/backups/identity/export');
    expect(req.request.body).toEqual({ confirm: true });
    req.flush(EXPORT);
    http.expectOne('/api/v1/backups/identity').flush({ ...IDENTITY, exported: true });
    fixture.detectChanges();
    expect(text()).toContain('AGE-SECRET-KEY-TEST');
    expect(text()).toContain('Store this offline');
    identityPanel().dismissExport();
    fixture.detectChanges();
    expect(text()).not.toContain('AGE-SECRET-KEY-TEST');
  });

  const ROTATE = {
    ...EXPORT,
    identity: 'AGE-SECRET-KEY-ROTATED',
    fingerprint: 'cc33',
    previous_fingerprint: 'aa11',
    rewrap_operation_id: 0,
    runs_note: 'existing runs are being re-wrapped to the new key in the background',
  };

  it('rotates the identity only after confirmation and shows the new key once', () => {
    load([]);
    identityPanel().askRotate();
    fixture.detectChanges();
    expect(text()).toContain('Nothing is deleted');
    http.expectNone('/api/v1/backups/identity/rotate');
    identityPanel().confirmRotate();
    const req = http.expectOne('/api/v1/backups/identity/rotate');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ confirm: true });
    req.flush(ROTATE);
    http.expectOne('/api/v1/backups/identity').flush({ ...IDENTITY, exported: true });
    fixture.detectChanges();
    expect(text()).toContain('AGE-SECRET-KEY-ROTATED');
    expect(text()).toContain('re-wrapped to the new key in the background');
    identityPanel().acknowledgeExport();
    http.expectOne('/api/v1/backups/identity/ack').flush({ ...IDENTITY, exported: true, acknowledged: true });
    http.expectOne('/api/v1/backups/identity').flush({ ...IDENTITY, exported: true, acknowledged: true });
    fixture.detectChanges();
    expect(text()).not.toContain('AGE-SECRET-KEY-ROTATED');
  });

  it('re-wraps existing backups without minting a key and shows the last result', () => {
    load([]);
    identityPanel().rewrap();
    const req = http.expectOne('/api/v1/backups/identity/rewrap');
    expect(req.request.method).toBe('POST');
    req.flush({ id: 0 });
    http
      .expectOne('/api/v1/backups/identity')
      .flush({
        ...IDENTITY,
        retired_unreadable: 1,
        rewrap: { fingerprint: 'cc33', finished_at: '2026-11-11T10:00:00Z', rewrapped: 2, already_current: 1, failed: ['pkg/run/x.age'], failed_count: 1, complete: false },
      });
    fixture.detectChanges();
    expect(text()).toContain('1 file(s) could not be re-wrapped');
    expect(text()).toContain('pkg/run/x.age');
    expect(text()).toContain('1 retired recovery key(s)');
    expect(text()).not.toContain('AGE-SECRET-KEY');
  });

  it('shows a refused rotation as an error and keeps no key', () => {
    load([]);
    identityPanel().confirmRotate();
    http.expectOne('/api/v1/backups/identity/rotate').flush('too many recovery identity exports this hour', {
      status: 429,
      statusText: 'Too Many Requests',
    });
    fixture.detectChanges();
    expect(text()).toContain('too many');
    expect(text()).not.toContain('AGE-SECRET-KEY');
  });

  it('keeps the exported key out of storage and drops it on ack or dismiss', () => {
    load([]);
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    identityPanel().confirmExport();
    http.expectOne('/api/v1/backups/identity/export').flush(EXPORT);
    http.expectOne('/api/v1/backups/identity').flush({ ...IDENTITY, exported: true });
    fixture.detectChanges();
    expect(text()).toContain('AGE-SECRET-KEY-TEST');
    identityPanel().acknowledgeExport();
    const ack = http.expectOne('/api/v1/backups/identity/ack');
    expect(ack.request.method).toBe('POST');
    ack.flush({ ...IDENTITY, exported: true, acknowledged: true });
    http.expectOne('/api/v1/backups/identity').flush({ ...IDENTITY, exported: true, acknowledged: true });
    fixture.detectChanges();
    expect(text()).not.toContain('AGE-SECRET-KEY-TEST');
    expect(identityPanel().exported()).toBeNull();
    expect(setItem).not.toHaveBeenCalled();
    expect(JSON.stringify({ ...localStorage })).not.toContain('AGE-SECRET-KEY');
    setItem.mockRestore();
  });

  it('shows schedule problems next to each field', () => {
    load([]);
    save('', '0', 'x', '');
    const el = fixture.nativeElement as HTMLElement;
    const hours = el.querySelector('#sched-interval') as HTMLInputElement;
    expect(hours.getAttribute('aria-invalid')).toBe('true');
    expect(hours.getAttribute('aria-describedby')).toBe('sched-interval-error');
    expect(el.querySelector('#sched-app-error')?.textContent).toContain('Enter the app');
    expect(el.querySelector('#sched-retain-error')?.textContent).toContain('Retain must be');
    expect(el.querySelectorAll('.field-error').length).toBe(4);
  });
});
