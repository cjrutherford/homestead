import { TestBed } from '@angular/core/testing';
import { Component } from '@angular/core';
import { AppIcon } from './app-icon';

@Component({
  imports: [AppIcon],
  template: `<hs-app-icon [icon]="icon" title="Jellyfin" />`,
})
class Host {
  icon = 'mdi:television-play';
}

describe('AppIcon', () => {
  function render(icon: string): HTMLElement {
    const fixture = TestBed.createComponent(Host);
    fixture.componentInstance.icon = icon;
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
});
