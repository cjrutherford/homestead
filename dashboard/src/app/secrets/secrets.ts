import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ApiService, SecretKeyStatus } from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';

/**
 * Secret key custody page (S258, ADR-021): shows the current secret key
 * version and offers a confirmed re-key. Re-key rewrites every stored
 * secret under a new key as an audited operation; progress lives on the
 * operation page. Key material never reaches the browser.
 */
@Component({
  imports: [OperationProgress, Loading, StateMessage, Dialog],
  selector: 'app-secrets',
  templateUrl: './secrets.html',
})
export class Secrets implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly status = signal<SecretKeyStatus | null>(null);
  protected readonly confirming = signal(false);
  protected readonly operationId = signal<number | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.secretKeyStatus().subscribe({
      next: (status) => {
        this.busy.set(false);
        this.status.set(status);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  askConfirm(): void {
    this.confirming.set(true);
  }

  cancel(): void {
    this.confirming.set(false);
  }

  rekey(): void {
    this.busy.set(true);
    this.error.set(null);
    this.operationId.set(null);
    this.api.secretRekey().subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.confirming.set(false);
        this.operationId.set(id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.confirming.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }
}
