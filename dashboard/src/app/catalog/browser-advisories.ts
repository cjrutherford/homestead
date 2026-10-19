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
        <h3>Browser advisories</h3>
        <app-state-message
          kind="notice"
          headline="Homestead cannot see your browser's capabilities."
          detail="These are advisories only. They never block install, update or start."
        />
        <dl class="advisory-list">
          @for (item of advisories(); track item.Capability) {
            <div>
              <dt>{{ item.Label || item.Capability }}</dt>
              <dd>{{ item.Reason }}</dd>
            </div>
          }
        </dl>
      </section>
    }
  `,
})
export class BrowserAdvisories {
  readonly advisories = input<BrowserAdvisory[] | null | undefined>(null);
}
