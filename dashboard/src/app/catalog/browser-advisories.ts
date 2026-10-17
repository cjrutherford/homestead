import { Component, input } from '@angular/core';
import { BrowserAdvisory } from '../core/api.service';
import { StateMessage } from '../state/state-message';

/** Plain-language names for the capability slugs the catalog uses (S252). */
const LABELS: Record<string, string> = {
  webcodecs: 'Video codecs (WebCodecs)',
  webgpu: 'GPU graphics (WebGPU)',
  webgl2: 'Graphics (WebGL2)',
  webaudio: 'Audio engine (Web Audio)',
  'audio-output': 'Audio output device',
  'secure-context': 'Secure connection (HTTPS)',
  clipboard: 'Clipboard access',
  'browser-storage': 'Browser storage',
};

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
              <strong>{{ label(item.Capability) }}</strong> &mdash; {{ item.Reason }}
            </li>
          }
        </ul>
      </section>
    }
  `,
})
export class BrowserAdvisories {
  readonly advisories = input<BrowserAdvisory[] | null | undefined>(null);

  protected label(capability: string): string {
    return LABELS[capability] ?? capability;
  }
}
