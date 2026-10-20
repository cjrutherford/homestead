import { Component, input } from '@angular/core';

/**
 * The guidance every wizard step opens with (S291, ADR-036): what the step
 * does in plain words, then the ordered tasks the owner does outside
 * Homestead, projected as list items. A step with nothing outside leaves the
 * projection empty and the list is not shown.
 */
@Component({
  selector: 'app-step-guide',
  template: `
    <p class="step-what"><strong>What this does.</strong> {{ what() }}</p>
    @if (outside()) {
      <h3>Outside Homestead</h3>
      <p class="hint">{{ outside() }}</p>
      <ol class="outside-tasks" aria-label="Outside Homestead">
        <ng-content />
      </ol>
    }
  `,
})
export class StepGuide {
  readonly what = input.required<string>();
  /** One line introducing the checklist; empty hides it. */
  readonly outside = input('');
}
