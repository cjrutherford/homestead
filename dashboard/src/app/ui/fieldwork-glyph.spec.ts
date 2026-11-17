import { TestBed } from '@angular/core/testing';
import { FieldworkGlyph } from './fieldwork-glyph';

describe('FieldworkGlyph', () => {
  it('loads the named local vector as a decorative currentColor mask', () => {
    const fixture = TestBed.createComponent(FieldworkGlyph);
    fixture.componentRef.setInput('name', 'people');
    fixture.detectChanges();

    const glyph = fixture.nativeElement.querySelector('span') as HTMLElement;
    expect(glyph.getAttribute('aria-hidden')).toBe('true');
    expect(glyph.style.getPropertyValue('--glyph-mask')).toBe('url(/icons/fieldwork/people.svg)');
  });
});
