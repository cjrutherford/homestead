import { Component, computed, input } from '@angular/core';
import { APP_ICONS } from './app-icons';

const CATALOG_ART: Readonly<Record<string, string>> = {
  nextcloud: 'nextcloud',
  immich: 'immich',
  jellyfin: 'jellyfin',
  'paperless-ngx': 'paperless-ngx',
  vaultwarden: 'vaultwarden',
};

/**
 * A supplied Fieldwork illustration, when one matches a current package ID;
 * otherwise the manifest's `mdi:<name>` is drawn in the current text colour.
 * Both are decorative because the app name sits beside the icon.
 */
@Component({
  selector: 'hs-app-icon',
  template: `
    @if (catalogArt(); as art) {
      <span
        class="hs-app-icon-art"
        [style.--icon-mask]="'url(/catalog-icons/' + art + '-mono.svg)'"
      ></span>
    } @else if (path(); as d) {
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
    .hs-app-icon-art {
      display: block;
      width: 72%;
      aspect-ratio: 1;
      background: currentColor;
      mask: var(--icon-mask) center / contain no-repeat;
      -webkit-mask: var(--icon-mask) center / contain no-repeat;
    }
  `,
  host: { 'aria-hidden': 'true' },
})
export class AppIcon {
  readonly icon = input<string | null | undefined>('');
  readonly name = input<string | null | undefined>('');
  readonly title = input<string>('');

  protected readonly catalogArt = computed(() => {
    const name = this.name() ?? '';
    return Object.hasOwn(CATALOG_ART, name) ? CATALOG_ART[name] : '';
  });
  protected readonly path = computed(() => {
    const name = (this.icon() ?? '').replace(/^mdi:/, '');
    return Object.hasOwn(APP_ICONS, name) ? APP_ICONS[name] : '';
  });
  protected readonly initial = computed(() => this.title().charAt(0).toUpperCase());
}
