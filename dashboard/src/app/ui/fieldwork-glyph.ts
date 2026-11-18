import { Component, computed, input } from '@angular/core';

/** Supporting navigation glyph from the Fieldwork asset family. */
@Component({
  selector: 'hs-fieldwork-glyph',
  template: `<span aria-hidden="true" [style.--glyph-mask]="mask()"></span>`,
  styles: `
    :host { display: inline-grid; flex: none; place-items: center; width: 1.5rem; height: 1.5rem; }
    span {
      display: block;
      width: 100%;
      height: 100%;
      background: currentColor;
      mask: var(--glyph-mask) center / contain no-repeat;
      -webkit-mask: var(--glyph-mask) center / contain no-repeat;
    }
  `,
})
export class FieldworkGlyph {
  readonly name = input.required<string>();
  protected readonly mask = computed(() => `url(/icons/fieldwork/${this.name()}.svg)`);
}
