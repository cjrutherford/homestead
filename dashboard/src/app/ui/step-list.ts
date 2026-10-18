import { Component, input } from '@angular/core';
import { OperationStep } from '../core/api.service';

const WORDS: Record<string, string> = {
  succeeded: 'Done',
  running: 'Running',
  pending: 'Waiting',
  failed: 'Failed',
  cancelled: 'Cancelled',
};

/**
 * The step list every operation view shares (S283): the rail, the operation
 * detail page and in-page progress. Each step shows a glyph and the word, so
 * state never relies on colour alone.
 */
@Component({
  selector: 'app-step-list',
  template: `
    @if (steps().length === 0) {
      <p class="step-empty">No steps recorded yet.</p>
    } @else {
      <ol class="step-list" [class.step-list-compact]="compact()" aria-label="Steps">
        @for (step of steps(); track step.id) {
          <li class="step" [attr.data-status]="step.status">
            <span class="step-glyph" aria-hidden="true"></span>
            <span class="step-name">{{ step.name }}</span>
            <span class="step-status">{{ word(step.status) }}</span>
            @if (step.error) {
              <span class="step-error">{{ step.error }}</span>
            }
          </li>
        }
      </ol>
    }
  `,
})
export class StepList {
  readonly steps = input<OperationStep[]>([]);
  readonly compact = input(false);

  protected word(status: string): string {
    return WORDS[status] ?? status;
  }
}
