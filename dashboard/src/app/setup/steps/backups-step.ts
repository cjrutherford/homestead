import { Component } from '@angular/core';
import { NightlyPolicy } from '../../backups/nightly-policy';
import { StepGuide } from './step-guide';

/**
 * Step 6 (S297, ADR-039 §2): the nightly backup policy, proposed with
 * defaults. Approving it, on or off, completes the step; the Backups page
 * offers the same policy later.
 */
@Component({
  imports: [NightlyPolicy, StepGuide],
  selector: 'app-setup-backups-step',
  template: `
    <app-step-guide
      what="Back up every app that keeps data, every night, to the disk you gave the backup class. Apps you install later are covered too. Apps with their own backup schedule keep it."
    />
    <p class="hint">
      Backups stay on the appliance's backup disk. To read them after losing the appliance, add a recipient whose
      key you hold, or export the recovery identity, on the Backups page.
    </p>
    <app-nightly-policy />
  `,
})
export class BackupsStep {}
