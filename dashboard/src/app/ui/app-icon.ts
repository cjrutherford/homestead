import { Component, computed, input } from '@angular/core';
import { APP_ICONS } from './app-icons';

/**
 * An app's icon (S340): the manifest's `mdi:<name>` drawn as an inline SVG
 * path in the current text colour, decorative (the app's name sits beside
 * it). An unknown or empty name falls back to the first letter of the title.
 */
@Component({
  selector: 'hs-app-icon',
  template: `
    @if (path(); as d) {
      <svg class="hs-app-icon-svg" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
        <path [attr.d]="d" fill="currentColor" />
      </svg>
    } @else {
      <span aria-hidden="true">{{ initial() }}</span>
    }
  `,
  styles: `
    :host { display: inline-grid; place-items: center; }
    .hs-app-icon-svg { width: 60%; height: 60%; }
  `,
  host: { 'aria-hidden': 'true' },
})
export class AppIcon {
  readonly icon = input<string | null | undefined>('');
  readonly title = input<string>('');

  protected readonly path = computed(() => {
    const name = (this.icon() ?? '').replace(/^mdi:/, '');
    return Object.hasOwn(APP_ICONS, name) ? APP_ICONS[name] : '';
  });
  protected readonly initial = computed(() => this.title().charAt(0).toUpperCase());
}
