import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import {
  ApiService,
  BackupSchedule,
  SchedulerJob,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';

/**
 * Backup surfaces (S267): resolved destination plus schedule records,
 * with the same capabilities as `homesteadctl backup`. Intervals are
 * entered in hours (minimum 1) and sent as seconds. The daemon
 * scheduler fires schedules (S260); these controls configure, and the
 * read-only scheduled-jobs panel shows its state. Retain 0 keeps every
 * run (server semantics).
 */
@Component({
  imports: [StateMessage],
  selector: 'app-backups',
  templateUrl: './backups.html',
  styles: ['.backup-root { overflow-wrap: anywhere; }'],
})
export class Backups implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly destination = signal<string | null>(null);
  protected readonly encryption = signal<string | null>(null);
  protected readonly destinationError = signal<string | null>(null);
  protected readonly schedules = signal<BackupSchedule[]>([]);
  protected readonly inspected = signal<BackupSchedule | null>(null);
  protected readonly jobs = signal<SchedulerJob[]>([]);
  protected readonly jobsLoading = signal(true);
  protected readonly jobsError = signal<string | null>(null);
  protected readonly confirmingRemove = signal<string | null>(null);
  protected readonly loading = signal(true);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.loading.set(true);
    this.error.set(null);
    this.destinationError.set(null);
    this.api.backupDestination().subscribe({
      next: ({ destination, encrypted, format_version }) => {
        this.destination.set(destination);
        this.encryption.set(
          encrypted ? `age, format ${format_version ?? 2}` : 'not encrypted',
        );
      },
      error: (failure: HttpErrorResponse) => {
        this.destination.set(null);
        this.destinationError.set(failure.error ?? failure.message);
      },
    });
    this.jobsLoading.set(true);
    this.jobsError.set(null);
    this.api.schedulerJobs().subscribe({
      next: (rows) => {
        this.jobsLoading.set(false);
        this.jobs.set(rows);
      },
      error: (failure: HttpErrorResponse) => {
        this.jobsLoading.set(false);
        this.jobsError.set(failure.error ?? failure.message);
      },
    });
    this.api.backupSchedules().subscribe({
      next: (rows) => {
        this.loading.set(false);
        this.schedules.set(rows);
      },
      error: (failure: HttpErrorResponse) => {
        this.loading.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  inspect(name: string): void {
    this.error.set(null);
    this.api.backupScheduleGet(name).subscribe({
      next: (schedule) => this.inspected.set(schedule),
      error: (failure: HttpErrorResponse) =>
        this.error.set(failure.error ?? failure.message),
    });
  }

  save(
    name: string,
    hours: string,
    retain: string,
    enabled: boolean,
    actor: string,
  ): void {
    name = name.trim();
    actor = actor.trim();
    hours = hours.trim();
    retain = retain.trim();
    if (!name || !actor) {
      this.error.set('A schedule needs a package and a named actor.');
      return;
    }
    const interval = /^\d+$/.test(hours) ? Number(hours) : NaN;
    if (!Number.isSafeInteger(interval) || interval < 1) {
      this.error.set('Interval must be a whole number of hours, at least 1.');
      return;
    }
    const keep = retain === '' ? 7 : /^\d+$/.test(retain) ? Number(retain) : NaN;
    if (!Number.isSafeInteger(keep)) {
      this.error.set('Retain must be a whole number of runs, 0 or more.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api
      .backupScheduleSave({
        package: name,
        interval_seconds: interval * 3600,
        retain: keep,
        enabled,
        actor,
      })
      .subscribe({
        next: (saved) => {
          this.busy.set(false);
          this.notice.set(`Saved schedule for ${saved.package}.`);
          this.refresh();
        },
        error: (failure: HttpErrorResponse) => {
          this.busy.set(false);
          this.error.set(failure.error ?? failure.message);
        },
      });
  }

  /** First step of removal: ask the owner to confirm. */
  askRemove(name: string): void {
    this.confirmingRemove.set(name);
  }

  cancelRemove(): void {
    this.confirmingRemove.set(null);
  }

  /** Whole hours read plainly; other cadences show up to two decimals. */
  hours(seconds: number): string {
    return `${Number((seconds / 3600).toFixed(2))}h`;
  }

  /** Go's zero time means the event never happened. */
  when(iso: string, empty = 'never'): string {
    return !iso || iso.startsWith('0001-') ? empty : iso;
  }

  remove(name: string): void {
    this.confirmingRemove.set(null);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.backupScheduleDelete(name).subscribe({
      next: () => {
        this.busy.set(false);
        if (this.inspected()?.package === name) {
          this.inspected.set(null);
        }
        this.notice.set(`Removed schedule for ${name}.`);
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }
}
