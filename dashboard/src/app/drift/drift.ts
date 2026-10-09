import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, FileDrift, PackageSummary } from '../core/api.service';

/**
 * Drift review page (S213): per-package managed-state review with
 * accept and restore choices. Accept adopts current files as
 * managed; restore rewrites recorded files and converges the stack
 * through the operation engine, linking out to operation detail for
 * progress. This page never changes anything by itself.
 */
@Component({
  imports: [RouterLink],
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

  ngOnInit(): void {
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

  select(name: string): void {
    this.selected.set(name);
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
