import { Component, signal } from '@angular/core';
import { StateKind, StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';

/**
 * Validation board (S248, S282): every design-system primitive rendered
 * under every theme on one page, so regressions in tokens, state
 * components, and responsive rules are visible (and testable) in one
 * place. Static and API-free: it needs no session.
 */
@Component({
  imports: [StateMessage, Dialog],
  selector: 'app-validation-board',
  templateUrl: './validation-board.html',
  styles: ['nav a { margin-right: var(--spacing-md); }'],
})
export class ValidationBoard {
  readonly themes = ['light', 'dark', 'high-contrast'] as const;
  readonly kinds: StateKind[] = ['empty', 'loading', 'error', 'status'];
  /** The theme whose sample dialog is open, if any. */
  protected readonly dialogTheme = signal<string | null>(null);
}
