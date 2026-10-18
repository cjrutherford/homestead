import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Component, signal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { Loading, SKELETON_DELAY_MS, SKELETON_MIN_MS } from './loading';

@Component({
  imports: [Loading],
  template: `<app-loading [loading]="loading()" shape="list"><p class="content">Ready</p></app-loading>`,
})
class Host {
  readonly loading = signal(true);
}

describe('Loading (skeleton timing)', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  function setup() {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    const root: HTMLElement = fixture.nativeElement;
    return {
      fixture,
      skeleton: () => root.querySelector('.skeleton'),
      content: () => root.querySelector<HTMLElement>('.loading-content'),
      set(value: boolean) {
        fixture.componentInstance.loading.set(value);
        fixture.detectChanges();
      },
      advance(ms: number) {
        vi.advanceTimersByTime(ms);
        fixture.detectChanges();
      },
    };
  }

  it('shows no skeleton before the delay and none at all for a fast response', () => {
    const t = setup();
    t.advance(SKELETON_DELAY_MS - 10);
    expect(t.skeleton()).toBeNull();
    expect(t.content()?.hidden).toBe(true);
    t.set(false);
    t.advance(1000);
    expect(t.skeleton()).toBeNull();
    expect(t.content()?.hidden).toBe(false);
  });

  it('shows the skeleton after the delay and keeps it for the minimum time', () => {
    const t = setup();
    t.advance(SKELETON_DELAY_MS + 1);
    expect(t.skeleton()).not.toBeNull();
    expect(t.fixture.nativeElement.querySelector('[role="status"]')?.textContent).toContain('Loading');
    // Data arrives right after the skeleton appeared: it must stay.
    t.advance(50);
    t.set(false);
    expect(t.skeleton()).not.toBeNull();
    expect(t.content()?.hidden).toBe(true);
    t.advance(SKELETON_MIN_MS - 50 - 5);
    expect(t.skeleton()).not.toBeNull();
    t.advance(10);
    expect(t.skeleton()).toBeNull();
    expect(t.content()?.hidden).toBe(false);
  });

  it('removes the skeleton at once when it has already been shown long enough', () => {
    const t = setup();
    t.advance(SKELETON_DELAY_MS + SKELETON_MIN_MS + 100);
    t.set(false);
    expect(t.skeleton()).toBeNull();
    expect(t.content()?.hidden).toBe(false);
  });

  it('marks the region busy while loading', () => {
    const t = setup();
    expect(t.fixture.nativeElement.querySelector('app-loading').getAttribute('aria-busy')).toBe('true');
    t.set(false);
    expect(t.fixture.nativeElement.querySelector('app-loading').getAttribute('aria-busy')).toBeNull();
  });
});
