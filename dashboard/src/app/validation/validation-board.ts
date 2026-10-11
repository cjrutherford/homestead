import { Component } from '@angular/core';
import { StateKind, StateMessage } from '../state/state-message';

/**
 * Validation board (S248): every design-system primitive rendered
 * under every theme on one page, so regressions in tokens, state
 * components, and responsive rules are visible (and testable) in one
 * place. Static and API-free: it needs no session.
 */
@Component({
  imports: [StateMessage],
  selector: 'app-validation-board',
  templateUrl: './validation-board.html',
})
export class ValidationBoard {
  readonly themes = ['light', 'dark', 'high-contrast'] as const;
  readonly kinds: StateKind[] = ['empty', 'loading', 'error', 'status'];
}
