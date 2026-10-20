import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import {
  ApiService,
  BackupRecipients,
  BackupSchedule,
  SchedulerJob,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { RecoveryIdentity } from './recovery-identity';
import { formatWhen, WhenPipe } from '../core/format-time';
import { Loading } from '../ui/loading';

/**
 * Backup surfaces (S267): resolved destination plus schedule records,
 * with the same capabilities as `homesteadctl backup`. Intervals are
 * entered in hours (minimum 1) and sent as seconds. The daemon
 * scheduler fires schedules (S260); these controls configure, and the
 * read-only scheduled-jobs panel shows its state. Retain 0 keeps every
 * run (server semantics).
 */
@Component({
  imports: [Loading, StateMessage, Dialog, RecoveryIdentity, WhenPipe],
  selector: 'app-backups',
  templateUrl: './backups.html',
  styles: ['.backup-root { overflow-wrap: anywhere; }'],
})
export class Backups implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly fieldErrors = signal<Record<string, string>>({});
  protected readonly destination = signal<string | null>(null);
  protected readonly encryption = signal<string | null>(null);
  protected readonly destinationError = signal<string | null>(null);
  /** 422: no storage holds the backup class yet. A state, not a failure. */
  protected readonly destinationUnset = signal(false);
  protected readonly schedules = signal<BackupSchedule[]>([]);
  protected readonly inspected = signal<BackupSchedule | null>(null);
  protected readonly jobs = signal<SchedulerJob[]>([]);
  protected readonly jobsLoading = signal(true);
  protected readonly jobsError = signal<string | null>(null);
  protected readonly confirmingRemove = signal<string | null>(null);
  protected readonly recipients = signal<BackupRecipients | null>(null);
  protected readonly recipientsError = signal<string | null>(null);
  protected readonly confirmingRecipient = signal<string | null>(null);
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
    this.destinationUnset.set(false);
    this.api.backupDestination().subscribe({
      next: ({ destination, encrypted, format_version }) => {
        this.destination.set(destination);
        this.encryption.set(
          encrypted ? `age, format ${format_version ?? 2}` : 'not encrypted',
        );
      },
      error: (failure: HttpErrorResponse) => {
        this.destination.set(null);
        if (failure.status === 422) {
          this.destinationUnset.set(true);
          return;
        }
        this.destinationError.set(failure.error ?? failure.message);
      },
    });
    this.recipientsError.set(null);
    this.api.backupRecipients().subscribe({
      next: (view) => this.recipients.set(view),
      error: (failure: HttpErrorResponse) =>
        this.recipientsError.set(failure.error ?? failure.message),
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
    const problems: Record<string, string> = {};
    if (!name) {
      problems['app'] = 'Enter the app to back up.';
    }
    if (!actor) {
      problems['actor'] = 'Enter the name of the owner making this change.';
    }
    const interval = /^\d+$/.test(hours) ? Number(hours) : NaN;
    if (!Number.isSafeInteger(interval) || interval < 1) {
      problems['interval'] = 'Interval must be a whole number of hours, at least 1.';
    }
    const keep = retain === '' ? 7 : /^\d+$/.test(retain) ? Number(retain) : NaN;
    if (!Number.isSafeInteger(keep)) {
      problems['retain'] = 'Retain must be a whole number of runs, 0 or more.';
    }
    this.fieldErrors.set(problems);
    if (Object.keys(problems).length > 0) {
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
    return formatWhen(iso, empty);
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

  addRecipient(key: string, label: string): void {
    key = key.trim();
    if (!/^age1[0-9a-z]+$/.test(key)) {
      this.error.set('Enter one age public key starting with age1.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api
      .backupRecipientAdd({ public_key: key, label: label.trim() })
      .subscribe({
        next: (added) => {
          this.busy.set(false);
          this.notice.set(
            `Added recipient ${added.fingerprint}. New backup runs include it; existing runs are not re-encrypted.`,
          );
          this.refresh();
        },
        error: (failure: HttpErrorResponse) => {
          this.busy.set(false);
          this.error.set(failure.error ?? failure.message);
        },
      });
  }

  askRemoveRecipient(fingerprint: string): void {
    this.confirmingRecipient.set(fingerprint);
  }

  cancelRemoveRecipient(): void {
    this.confirmingRecipient.set(null);
  }

  removeRecipient(fingerprint: string): void {
    this.confirmingRecipient.set(null);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.backupRecipientRemove(fingerprint).subscribe({
      next: () => {
        this.busy.set(false);
        this.notice.set(
          `Removed recipient ${fingerprint}. It can still read runs made while it was configured.`,
        );
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }
}
