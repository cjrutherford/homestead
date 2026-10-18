import { afterEach, describe, expect, it, vi } from 'vitest';
import { viewTransitionOptions } from './view-transition';

function stubMotion(reduce: boolean): void {
  vi.stubGlobal('matchMedia', (query: string) => ({ matches: reduce && query.includes('reduce') }));
}

describe('view transitions', () => {
  afterEach(() => vi.unstubAllGlobals());

  const created = (skip: () => void) =>
    viewTransitionOptions.onViewTransitionCreated?.({
      transition: { skipTransition: skip } as unknown as ViewTransition,
      from: null as never,
      to: null as never,
    });

  it('skips the transition under reduced motion', () => {
    stubMotion(true);
    const skip = vi.fn();
    created(skip);
    expect(skip).toHaveBeenCalledTimes(1);
  });

  it('lets the crossfade run otherwise', () => {
    stubMotion(false);
    const skip = vi.fn();
    created(skip);
    expect(skip).not.toHaveBeenCalled();
  });

  it('never animates the first load', () => {
    expect(viewTransitionOptions.skipInitialTransition).toBe(true);
  });
});
