import { Component, computed, input, output } from '@angular/core';
import { RouterLink } from '@angular/router';

export type StateKind = 'empty' | 'error' | 'status' | 'warning' | 'notice';

/**
 * Shared state message (S248, S283): one component for empty, error,
 * success, caution and neutral notices on every route. Kind drives the role
 * (errors alert; status, warning and notice announce politely; empty stays
 * silent) so pages share wording and assistive behaviour.
 *
 * Copy rules: an empty state invites the action, an error says what
 * happened and how to fix it. Give the next step as `actionLabel` with
 * either `actionLink` (a route) or the `action` output (a button).
 * A null or empty headline renders nothing, so callers can bind a
 * nullable signal directly.
 */
@Component({
  selector: 'app-state-message',
  imports: [RouterLink],
  templateUrl: './state-message.html',
})
export class StateMessage {
  readonly kind = input<StateKind>('empty');
  readonly headline = input<string | null | undefined>('');
  /** Optional second line: why, or how to fix it. */
  readonly detail = input<string | null | undefined>('');
  /** Label of the next action. Without it no action renders. */
  readonly actionLabel = input('');
  /** Route for the action; without it the action is a button emitting `action`. */
  readonly actionLink = input<string | unknown[] | null>(null);
  readonly action = output<void>();

  protected readonly role = computed(() => {
    switch (this.kind()) {
      case 'error':
        return 'alert';
      case 'status':
      case 'warning':
      case 'notice':
        return 'status';
      default:
        return null;
    }
  });
}
