import { ViewTransitionsFeatureOptions } from '@angular/router';

export const REDUCED_MOTION_QUERY = '(prefers-reduced-motion: reduce)';

export function prefersReducedMotion(): boolean {
  return typeof matchMedia === 'function' && matchMedia(REDUCED_MOTION_QUERY).matches;
}

/**
 * Route changes crossfade through the View Transitions API (S283). Angular
 * does nothing in browsers without support. Under reduced motion the
 * transition is skipped, so the page swaps instantly. The first load never
 * animates.
 */
export const viewTransitionOptions: ViewTransitionsFeatureOptions = {
  skipInitialTransition: true,
  onViewTransitionCreated: ({ transition }) => {
    if (prefersReducedMotion()) {
      transition.skipTransition();
    }
  },
};
