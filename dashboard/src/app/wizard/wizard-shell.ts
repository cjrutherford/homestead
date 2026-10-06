import { Component, input, output } from '@angular/core';

/**
 * Reusable wizard shell (S036): step indicator with progress, projected
 * step content, and Back navigation. Individual wizards own their forms,
 * validation, and transitions; the shell only frames them. Forward motion
 * stays inside step content (each step submits itself).
 */
@Component({
  selector: 'app-wizard-shell',
  templateUrl: './wizard-shell.html',
})
export class WizardShell {
  /** Ordered step titles. */
  readonly steps = input.required<string[]>();
  /** Zero-based index of the current step. */
  readonly current = input.required<number>();
  /** Emitted when Back is pressed; the wizard decides the previous step. */
  readonly back = output<void>();
}
