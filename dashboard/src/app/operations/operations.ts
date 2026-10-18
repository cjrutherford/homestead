import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, OperationState } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { labelForType } from '../core/operation-labels';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
const BADGES: Record<string, string> = {
  succeeded: 'badge-ok',
  failed: 'badge-fail',
  running: 'badge-running',
  pending: 'badge-pending',
};
const WORDS: Record<string, string> = {
  succeeded: 'Done',
  failed: 'Failed',
  running: 'Running',
  pending: 'Waiting',
  cancelled: 'Cancelled',
};

/** Operations list page (S064, S283): newest-first records linking to detail. */
@Component({
  imports: [StateMessage, RouterLink, Loading],
  selector: 'app-operations',
  templateUrl: './operations.html',
})
export class Operations implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly operations = signal<OperationState[] | null>(null);
  protected readonly error = signal<string | null>(null);

  ngOnInit(): void {
    this.load();
  }

  protected load(): void {
    this.error.set(null);
    this.api.operations().subscribe({
      next: (operations) => this.operations.set(operations),
      error: (err: HttpErrorResponse) =>
        this.error.set(httpErrorMessage(err, 'Loading operations')),
    });
  }

  protected labelOf(type: string): string {
    return labelForType(type).title;
  }

  protected badgeOf(status: string): string {
    return BADGES[status] ?? '';
  }

  protected wordOf(status: string): string {
    return WORDS[status] ?? status;
  }
}
