import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RESTORE_WAIT_MS, RestoreTarget, restoreScrollWhenTall } from './scroll-restore';

function fakeTarget(height: number): RestoreTarget & { height: number; scrolled: number[]; fire: (t: string) => void } {
  const handlers = new Map<string, () => void>();
  const target = {
    height,
    scrolled: [] as number[],
    scrollY: 0,
    innerHeight: 800,
    document: { documentElement: { get scrollHeight() { return target.height; } } },
    scrollTo: (o: ScrollToOptions) => void target.scrolled.push(o.top ?? 0),
    addEventListener: (t: string, fn: () => void) => void handlers.set(t, fn),
    removeEventListener: (t: string) => void handlers.delete(t),
    fire: (t: string) => handlers.get(t)?.(),
  };
  return target as never;
}

describe('restoreScrollWhenTall', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('waits for the page to grow, then restores once', () => {
    const t = fakeTarget(900);
    restoreScrollWhenTall(t, 400);
    vi.advanceTimersByTime(300);
    expect(t.scrolled).toEqual([]);
    t.height = 1500;
    vi.advanceTimersByTime(100);
    expect(t.scrolled).toEqual([400]);
    vi.advanceTimersByTime(1000);
    expect(t.scrolled).toEqual([400]);
  });

  it('gives up after the wait and never restores a short page', () => {
    const t = fakeTarget(900);
    restoreScrollWhenTall(t, 400);
    vi.advanceTimersByTime(RESTORE_WAIT_MS + 500);
    t.height = 2000;
    vi.advanceTimersByTime(500);
    expect(t.scrolled).toEqual([]);
  });

  it('stops when the owner scrolls', () => {
    const t = fakeTarget(900);
    restoreScrollWhenTall(t, 400);
    t.fire('wheel');
    t.height = 2000;
    vi.advanceTimersByTime(500);
    expect(t.scrolled).toEqual([]);
  });

  it('does nothing for the top of a page', () => {
    const t = fakeTarget(900);
    restoreScrollWhenTall(t, 0);
    vi.advanceTimersByTime(500);
    expect(t.scrolled).toEqual([]);
  });
});
