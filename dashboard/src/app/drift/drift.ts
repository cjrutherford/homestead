import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, DriftSchedule, FileDrift, LiveDrift, PackageSummary } from '../core/api.service';
import { StateMessage } from '../state/state-message';

/**
 * Drift review page (S213): per-package managed-state review with
 * accept and restore choices. Accept adopts current files as
 * managed; restore rewrites recorded files and converges the stack
 * through the operation engine, linking out to operation detail for
 * progress. This page never changes anything by itself. The scan
 * panel (S261) shows the last scheduled scan and edits its cadence;
 * scheduled scans only detect, never resolve. Running containers
 * (S262) are compared read-only with the generated compose files; each
 * live finding names the flow that converges it.
 */
@Component({
  imports: [StateMessage, RouterLink],
  selector: 'app-drift',
  templateUrl: './drift.html',
})
export class Drift implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly packages = signal<PackageSummary[]>([]);
  protected readonly selected = signal<string | null>(null);
  protected readonly review = signal<FileDrift[]>([]);
  protected readonly operationId = signal<number | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly live = signal<LiveDrift | null>(null);
  protected readonly schedule = signal<DriftSchedule | null>(null);

  ngOnInit(): void {
    this.api.driftSchedule().subscribe({
      next: (schedule) => this.schedule.set(schedule),
      error: (failure: HttpErrorResponse) =>
        this.error.set(failure.error ?? failure.message),
    });
    this.busy.set(true);
    this.api.catalogPackages().subscribe({
      next: (packages) => {
        this.busy.set(false);
        this.packages.set(packages);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.message);
      },
    });
  }

  saveSchedule(minutes: string, enabled: boolean): void {
    const wanted = Math.round(Number(minutes));
    const current = this.schedule();
    // An untouched field keeps the stored seconds exactly, even when the
    // interval (set over the CLI) is not a whole number of minutes.
    const interval_seconds =
      current !== null && wanted === this.minutes(current.interval_seconds)
        ? current.interval_seconds
        : wanted * 60;
    this.error.set(null);
    this.api.driftScheduleSave({ interval_seconds, enabled }).subscribe({
      next: (schedule) => this.schedule.set(schedule),
      error: (failure: HttpErrorResponse) =>
        this.error.set(failure.error ?? failure.message),
    });
  }

  /** Whole minutes shown in the form; seconds-exact when divisible by 60. */
  protected minutes(seconds: number): number {
    return Math.round(seconds / 60);
  }

  /** Human label that never rounds: exact minutes, with hours when whole. */
  protected every(seconds: number): string {
    if (seconds % 3600 === 0) {
      return `${seconds / 3600} hour(s)`;
    }
    if (seconds % 60 === 0) {
      return `${seconds / 60} minute(s)`;
    }
    return `${seconds} second(s)`;
  }

  /** True when restore (compose up) can fix a live finding by itself. */
  protected convergable(): boolean {
    return (this.live()?.findings ?? []).some((f) => f.resolution === 'restore');
  }

  select(name: string): void {
    this.selected.set(name);
    this.live.set(null);
    this.operationId.set(null);
    this.notice.set(null);
    this.refresh();
  }

  refresh(): void {
    const name = this.selected();
    if (name === null) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.driftLive(name).subscribe({
      next: (live) => this.live.set(live),
      error: (failure: HttpErrorResponse) => {
        this.live.set(null);
        this.error.set(failure.error ?? failure.message);
      },
    });
    this.api.driftReview(name).subscribe({
      next: (review) => {
        this.busy.set(false);
        this.review.set(review);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  accept(): void {
    const name = this.selected();
    if (name === null) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.driftResolve(name, 'accept').subscribe({
      next: ({ accepted }) => {
        this.busy.set(false);
        this.notice.set(`Adopted ${(accepted ?? []).length} file(s) as managed.`);
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  restore(): void {
    const name = this.selected();
    if (name === null) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.driftResolve(name, 'restore').subscribe({
      next: ({ id, detail }) => {
        this.busy.set(false);
        if ((id ?? 0) === 0) {
          this.notice.set(detail ?? 'No drift.');
          this.refresh();
          return;
        }
        this.operationId.set(id ?? null);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }
}
