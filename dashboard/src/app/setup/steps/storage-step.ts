import { Component } from '@angular/core';
import { StorageSetup } from '../../storage/storage';
import { StepGuide } from './step-guide';

/**
 * Step 2: the Storage page itself, embedded: discovered mounts, register,
 * and assign the app, bulk and backup classes. Done is derived from the app
 * class being assigned.
 */
@Component({
  imports: [StorageSetup, StepGuide],
  selector: 'app-setup-storage-step',
  template: `
    <app-step-guide
      what="Apps need somewhere to keep their data. You register a disk that is already mounted, then give it a class: app data first, and optionally bulk media and backups. Homestead never formats or partitions a disk."
      outside="A disk must already be mounted on the appliance before it shows up here."
    >
      <li>Attach the disk, and create a filesystem on it with your own tools.</li>
      <li>
        Mount it, and make the mount permanent in <code>/etc/fstab</code>, for example:
        <code>UUID=0a1b2c3d-1111-2222-3333-444455556666 /mnt/data ext4 defaults,nofail 0 2</code>
      </li>
      <li>Come back and press Refresh below. The mount appears under Discovered mounts.</li>
    </app-step-guide>
    <p class="hint">
      Next unlocks when the application class is assigned, or when you skip this step. Installing apps needs it.
    </p>
    <app-storage [embedded]="true" />
  `,
})
export class StorageStep {}
