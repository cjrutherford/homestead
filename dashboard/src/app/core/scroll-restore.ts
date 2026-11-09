import { Router, Scroll } from '@angular/router';

/** How long a restore keeps waiting for the returning page to grow tall enough. */
export const RESTORE_WAIT_MS = 2500;
const RESTORE_STEP_MS = 50;

export interface RestoreTarget {
  scrollTo(options: ScrollToOptions): void;
  readonly scrollY: number;
  readonly innerHeight: number;
  readonly document: { documentElement: { scrollHeight: number } };
  addEventListener(type: string, fn: () => void, options?: AddEventListenerOptions): void;
  removeEventListener(type: string, fn: () => void): void;
}

/**
 * Back and forward restore the earlier scroll position (S283 KVM review).
 * The router restores it the moment navigation ends, but pages load their
 * data after that and are still short, so the browser clamps the position
 * to the top. This keeps retrying until the page is tall enough (or the
 * owner scrolls, or RESTORE_WAIT_MS passes). Returns a cancel function.
 */
export function restoreScrollWhenTall(target: RestoreTarget, y: number): () => void {
  if (y <= 0) {
    return () => undefined;
  }
  let waited = 0;
  let timer: ReturnType<typeof setTimeout> | null = null;
  const stop = () => {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
    for (const type of ['wheel', 'touchstart', 'keydown']) {
      target.removeEventListener(type, stop);
    }
  };
  const step = () => {
    const room = target.document.documentElement.scrollHeight - target.innerHeight;
    if (room >= y) {
      target.scrollTo({ top: y, left: 0, behavior: 'instant' });
      stop();
      return;
    }
    waited += RESTORE_STEP_MS;
    if (waited >= RESTORE_WAIT_MS) {
      stop();
      return;
    }
    timer = setTimeout(step, RESTORE_STEP_MS);
  };
  for (const type of ['wheel', 'touchstart', 'keydown']) {
    target.addEventListener(type, stop, { passive: true, once: true });
  }
  step();
  return stop;
}

/** Wires the retry to the router's own restore events. */
export function watchScrollRestore(router: Router, target: RestoreTarget = window): void {
  let cancel: () => void = () => undefined;
  router.events.subscribe((event) => {
    if (event instanceof Scroll) {
      cancel();
      cancel = event.position ? restoreScrollWhenTall(target, event.position[1]) : () => undefined;
    }
  });
}
