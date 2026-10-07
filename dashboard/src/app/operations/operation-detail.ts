import { Component, DestroyRef, inject, OnInit, signal } from '@angular/core';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ApiService, OperationState } from '../core/api.service';

/**
 * Operation detail page (S064): initial fetch plus live state events
 * until terminal. The subscription dies with the component; terminal
 * states stop the stream server-side.
 */
@Component({
  imports: [RouterLink],
  selector: 'app-operation-detail',
  templateUrl: './operation-detail.html',
})
export class OperationDetail implements OnInit {
  private readonly api = inject(ApiService);
  private readonly route = inject(ActivatedRoute);
  private readonly destroyRef = inject(DestroyRef);

  protected readonly operation = signal<OperationState | null>(null);
  protected readonly missing = signal(false);

  ngOnInit(): void {
    const id = Number(this.route.snapshot.paramMap.get('id'));
    if (!Number.isInteger(id) || id <= 0) {
      this.missing.set(true);
      return;
    }
    this.api.operation(id).subscribe({
      next: (op) => {
        this.operation.set(op);
        if (!isTerminal(op.status)) {
          this.api
            .operationEvents(id)
            .pipe(takeUntilDestroyed(this.destroyRef))
            .subscribe({
              next: (update) => this.operation.set(update),
              error: () => undefined,
            });
        }
      },
      error: () => this.missing.set(true),
    });
  }
}

function isTerminal(status: string): boolean {
  return status === 'succeeded' || status === 'failed' || status === 'cancelled';
}
