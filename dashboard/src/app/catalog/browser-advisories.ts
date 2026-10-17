import { Component, input } from '@angular/core';
import { BrowserAdvisory } from '../core/api.service';
import { StateMessage } from '../state/state-message';

/**
 * Browser capability advisories (S252): non-blocking warnings with a
 * plain-language reason per capability. The appliance cannot see the
 * owner's browser, so nothing here gates install; the panel says so.
 * Renders nothing when the package declares no browser requirements.
 */
@Component({
  imports: [StateMessage],
  selector: 'app-browser-advisories',
  template: `
    @if (advisories()?.length) {
      <section class="browser-advisories" aria-label="Browser advisories">
        <h4>Browser advisories</h4>
        <app-state-message
          kind="status"
          headline="Homestead cannot see your browser's capabilities. These are advisories only and never block install, update or start."
        />
        <ul>
          @for (item of advisories(); track item.Capability) {
            <li>
              <strong>{{ item.Label || item.Capability }}</strong> &mdash; {{ item.Reason }}
            </li>
          }
        </ul>
      </section>
    }
  `,
})
export class BrowserAdvisories {
  readonly advisories = input<BrowserAdvisory[] | null | undefined>(null);
}
