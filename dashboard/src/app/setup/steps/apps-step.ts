import { Component, inject, input, signal } from '@angular/core';
import { InstallWizard } from '../../catalog/install-wizard';
import { SetupService, SetupStatus } from '../setup.service';
import { StepGuide } from './step-guide';

/**
 * Step 5: the curated starter apps, each installed through the install
 * wizard's own plan and review, embedded one app at a time.
 */
@Component({
  imports: [InstallWizard, StepGuide],
  selector: 'app-setup-apps-step',
  template: `
    <app-step-guide
      what="Pick a few apps to start with. Each install shows you its plan and what it will change before anything starts, and runs in the background. You can add or remove apps from the catalog at any time."
    />
    @if (installing(); as name) {
      <app-install-wizard [embedded]="true" [appName]="name" (closed)="installing.set(null)" />
    } @else {
      @if (status().starter_apps.length === 0) {
        <p>No starter apps to suggest. Browse the catalog from the Apps page after setup.</p>
      } @else {
        <ul aria-label="Starter apps">
          @for (app of status().starter_apps; track app.name) {
            <li>
              <strong>{{ app.title }}</strong>: {{ app.blurb }}
              @if (app.installed) {
                (installed)
              } @else {
                <button type="button" (click)="installing.set(app.name)">Install {{ app.title }}</button>
              }
            </li>
          }
        </ul>
      }
      <p class="hint">Installs run in the background. You do not have to wait for them here.</p>
      <p>
        <button type="button" [disabled]="setup.busy()" (click)="finish()">I am finished with apps</button>
      </p>
    }
  `,
})
export class AppsStep {
  protected readonly setup = inject(SetupService);
  readonly status = input.required<SetupStatus>();
  protected readonly installing = signal<string | null>(null);

  protected finish(): void {
    this.setup.change('apps', { state: 'done' }, 'Finished. Add apps any time from the catalog.');
  }
}
