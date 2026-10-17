import { describe, expect, it, beforeEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { BrowserAdvisories } from './browser-advisories';

describe('BrowserAdvisories', () => {
  let fixture: ComponentFixture<BrowserAdvisories>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [BrowserAdvisories],
    }).compileComponents();
    fixture = TestBed.createComponent(BrowserAdvisories);
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('lists each capability with its reason and the non-blocking caveat', () => {
    fixture.componentRef.setInput('advisories', [
      { Capability: 'webcodecs', Label: 'Video codecs (WebCodecs)', Reason: 'Hardware decoding.' },
      { Capability: 'audio-output', Label: 'Audio output device', Reason: 'Playback needs speakers.' },
    ]);
    fixture.detectChanges();
    expect(text()).toContain('Video codecs (WebCodecs)');
    expect(text()).toContain('Hardware decoding.');
    expect(text()).toContain('Audio output device');
    expect(text()).toContain('cannot see your browser');
    expect(text()).toContain('never block');
    expect(fixture.nativeElement.querySelector('[role="status"]')).not.toBeNull();
  });

  it('falls back to the slug when the API sends no label', () => {
    fixture.componentRef.setInput('advisories', [{ Capability: 'midi', Label: '', Reason: 'Keyboards.' }]);
    fixture.detectChanges();
    expect(text()).toContain('midi');
  });

  it('renders nothing without advisories', () => {
    fixture.componentRef.setInput('advisories', []);
    fixture.detectChanges();
    expect(text().trim()).toBe('');
    fixture.componentRef.setInput('advisories', null);
    fixture.detectChanges();
    expect(text().trim()).toBe('');
  });
});
