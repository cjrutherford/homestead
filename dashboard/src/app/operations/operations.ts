import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, OperationState } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { StateMessage } from '../state/state-message';

/** Operations list page (S064): newest-first records linking to detail. */
@Component({
  imports: [StateMessage, RouterLink],
  selector: 'app-operations',
  templateUrl: './operations.html',
})
export class Operations implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly operations = signal<OperationState[] | null>(null);
  protected readonly error = signal<string | null>(null);

  ngOnInit(): void {
    this.api.operations().subscribe({
      next: (operations) => this.operations.set(operations),
      error: (err: HttpErrorResponse) =>
        this.error.set(httpErrorMessage(err, 'Loading operations')),
    });
  }
}
