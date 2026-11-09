import { Component, inject } from '@angular/core';
import { ToastService } from '../core/toast.service';

/**
 * Toast and announcement region (S283), mounted once in the shell. The live
 * region announces politely and is always present, so assistive technology
 * hears text that changes; the toasts themselves carry no live role to avoid
 * reading twice.
 */
@Component({
  selector: 'app-toast-region',
  template: `
    <div class="visually-hidden" role="status" aria-live="polite" aria-atomic="true">
      {{ toasts.announcement() }}
    </div>
    <ul class="toasts" aria-label="Notifications">
      @for (toast of toasts.toasts(); track toast.id) {
        <li class="toast" [class]="'toast toast-' + toast.kind">
          <span class="toast-message">{{ toast.message }}</span>
          <button type="button" class="quiet toast-dismiss" (click)="toasts.dismiss(toast.id)">Dismiss</button>
        </li>
      }
    </ul>
  `,
})
export class ToastRegion {
  protected readonly toasts = inject(ToastService);
}
