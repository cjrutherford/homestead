import { Component, signal } from '@angular/core';
import { StateKind, StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Approval, Permit } from '../ui/permit';

/**
 * Validation board (S248, S282): every design-system primitive rendered
 * under every theme on one page, so regressions in tokens, state
 * components, and responsive rules are visible (and testable) in one
 * place. Static and API-free: it needs no session.
 */
@Component({
  imports: [StateMessage, Dialog, Permit],
  selector: 'app-validation-board',
  templateUrl: './validation-board.html',
  styles: [
    `
      nav a {
        margin-right: var(--spacing-md);
      }
      /* The four depths and the well (S310), each with its own token. */
      .depths {
        display: grid;
        grid-template-columns: minmax(7rem, 12rem) minmax(0, 1fr);
        margin: 0 0 var(--spacing-md);
        border-radius: var(--radius-panel);
        overflow: hidden;
      }
      .depth {
        padding: var(--spacing-md);
        font-size: var(--font-size-sm);
      }
      .depth-hedgerow {
        color: var(--on-hedgerow);
        background: var(--hedgerow);
        box-shadow: var(--edge-shadow);
      }
      .depth-ground {
        display: grid;
        gap: var(--spacing-md);
        background: var(--background);
      }
      .depth-plate {
        display: grid;
        gap: var(--spacing-sm);
        background: var(--surface);
        border: 1px solid var(--border);
        border-radius: var(--radius-panel);
        box-shadow: var(--elevation-plate);
      }
      .depth-well {
        background: var(--surface-sunken);
        border-radius: var(--radius-control);
        box-shadow: var(--elevation-well);
      }
      .depth-lifted {
        background: var(--surface-raised);
        border: 1px solid var(--border);
        border-radius: var(--radius-panel);
        box-shadow: var(--elevation-lifted);
      }
      @media (max-width: 39.9375rem) {
        .depths {
          grid-template-columns: minmax(0, 1fr);
        }
      }
    `,
  ],
})
export class ValidationBoard {
  readonly themes = ['dark', 'light', 'high-contrast'] as const;
  readonly kinds: StateKind[] = ['empty', 'error', 'status', 'warning', 'notice'];
  /** The theme whose sample dialog is open, if any. */
  protected readonly dialogTheme = signal<string | null>(null);
  /** Sample permit data (S309): the permit in each state, without a live plan. */
  protected readonly sampleDigest = 'sha256:9f2c4e1ab37d05c8e6f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7';
  protected readonly sampleApproval: Approval = { by: 'owner', at: '2026-10-25T14:02:00Z' };
}
