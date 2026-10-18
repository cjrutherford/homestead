import { Component, computed, input } from '@angular/core';

export type StateKind = 'empty' | 'loading' | 'error' | 'status' | 'warning';

/**
 * Shared state message (S248): one component for empty, loading,
 * error, and status states on every route. Kind drives the role —
 * errors alert, statuses and warnings announce, the rest stay neutral — so pages
 * share wording and assistive behavior instead of reinventing both.
 * A null or empty headline renders nothing, so callers can bind a
 * nullable signal directly.
 */
@Component({
  selector: 'app-state-message',
  templateUrl: './state-message.html',
})
export class StateMessage {
  readonly kind = input<StateKind>('empty');
  readonly headline = input<string | null | undefined>('');

  protected readonly role = computed(() => {
    switch (this.kind()) {
      case 'error':
        return 'alert';
      case 'status':
      case 'warning':
        return 'status';
      default:
        return null;
    }
  });
}
