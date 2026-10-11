import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ApiService, BackupSchedule } from '../core/api.service';
import { StateMessage } from '../state/state-message';

/**
 * Backup surfaces (S267): resolved destination plus schedule records,
 * with the same capabilities as `homesteadctl backup`. Intervals are
 * entered in hours (minimum 1) and sent as seconds. Run firing is
 * deferred; these controls configure only.
 */
@Component({
  imports: [StateMessage],
  selector: 'app-backups',
  templateUrl: './backups.html',
})
export class Backups implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly destination = signal<string | null>(null);
  protected readonly destinationError = signal<string | null>(null);
  protected readonly schedules = signal<BackupSchedule[]>([]);
  protected readonly inspected = signal<BackupSchedule | null>(null);
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
      next: ({ destination }) => this.destination.set(destination),
      error: (failure: HttpErrorResponse) => {
        this.destination.set(null);
        this.destinationError.set(failure.error ?? failure.message);
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
    const interval = Number(hours);
    const keep = Number(retain === '' ? '7' : retain);
    if (!name || !actor) {
      this.error.set('A schedule needs a package and a named actor.');
      return;
    }
    if (!Number.isInteger(interval) || interval < 1) {
      this.error.set('Interval must be a whole number of hours, at least 1.');
      return;
    }
    if (!Number.isInteger(keep) || keep < 0) {
      this.error.set('Retain must be a count of 0 or more.');
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
          this.notice.set(`Saved schedule for ${saved.Package}.`);
          this.refresh();
        },
        error: (failure: HttpErrorResponse) => {
          this.busy.set(false);
          this.error.set(failure.error ?? failure.message);
        },
      });
  }

  remove(name: string): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.backupScheduleDelete(name).subscribe({
      next: () => {
        this.busy.set(false);
        if (this.inspected()?.Package === name) {
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
