import { TestBed } from '@angular/core/testing';
import { Component } from '@angular/core';
import { AppIcon } from './app-icon';

@Component({
  imports: [AppIcon],
  template: `<hs-app-icon [icon]="icon" [name]="name" title="Jellyfin" />`,
})
class Host {
  icon = 'mdi:television-play';
  name = '';
}

describe('AppIcon', () => {
  function render(icon: string, name = ''): HTMLElement {
    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.icon = icon;
    fixture.componentInstance.name = name;
    fixture.detectChanges();
    return fixture.nativeElement as HTMLElement;
  }

  it('draws a known icon as an aria-hidden svg path', () => {
    const el = render('mdi:television-play');
    const d = el.querySelector('svg path')?.getAttribute('d') ?? '';
    expect(d.length).toBeGreaterThan(10);
    expect(el.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true');
    expect(el.textContent?.trim()).toBe('');
  });

  it('falls back to the monogram for an unknown or empty name', () => {
    for (const name of ['mdi:no-such-icon', '', 'constructor']) {
      const el = render(name);
      expect(el.querySelector('svg')).toBeNull();
      expect(el.textContent?.trim()).toBe('J');
    }
  });

  it('uses supplied monochrome artwork only for current catalog IDs', () => {
    const el = render('mdi:cloud', 'nextcloud');
    const art = el.querySelector('.hs-app-icon-art') as HTMLElement | null;
    expect(art?.style.getPropertyValue('--icon-mask')).toBe('url(/catalog-icons/nextcloud-mono.svg)');
    expect(el.querySelector('svg')).toBeNull();

    const paperless = render('mdi:file-document-multiple', 'paperless-ngx');
    expect((paperless.querySelector('.hs-app-icon-art') as HTMLElement).style.getPropertyValue('--icon-mask'))
      .toBe('url(/catalog-icons/paperless-ngx-mono.svg)');

    const provisional = render('mdi:cloud', 'home-assistant');
    expect(provisional.querySelector('.hs-app-icon-art')).toBeNull();
    expect(provisional.querySelector('svg path')?.getAttribute('d')).toBeTruthy();

    const inheritedKey = render('mdi:cloud', 'toString');
    expect(inheritedKey.querySelector('.hs-app-icon-art')).toBeNull();
    expect(inheritedKey.querySelector('svg path')?.getAttribute('d')).toBeTruthy();
  });
});
