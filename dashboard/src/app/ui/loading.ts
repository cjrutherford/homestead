import { Component, OnDestroy, computed, effect, input, signal } from '@angular/core';

/** A skeleton appears only if loading outlasts this, so fast responses never flash. */
export const SKELETON_DELAY_MS = 150;
/** Once shown, a skeleton stays at least this long, so it never flickers away. */
export const SKELETON_MIN_MS = 300;

export type SkeletonShape = 'lines' | 'list' | 'fields' | 'panel';

/**
 * Loading wrapper (S283): wrap the content that waits for data.
 *
 *   <app-loading [loading]="loading()" shape="list"> ...content... </app-loading>
 *
 * While `loading` is true the content is hidden. A content-shaped skeleton
 * appears after SKELETON_DELAY_MS and, once shown, stays for at least
 * SKELETON_MIN_MS; the content appears when both loading has ended and the
 * skeleton is done. The wrapper reserves the skeleton's height from the
 * start so the page does not jump when either one appears. Skeletons are
 * static (no shimmer): motion is reserved for the work-orders rail.
 */
@Component({
  selector: 'app-loading',
  host: {
    '[class.loading-busy]': 'hidden()',
    '[attr.data-shape]': 'shape()',
    '[attr.aria-busy]': 'loading() ? "true" : null',
  },
  template: `
    @if (showing()) {
      <div class="skeleton" aria-hidden="true">
        @switch (shape()) {
          @case ('list') {
            @for (row of rows; track row) {
              <div class="skeleton-bar skeleton-row"></div>
            }
          }
          @case ('fields') {
            @for (row of fieldRows; track row) {
              <div class="skeleton-bar skeleton-label"></div>
              <div class="skeleton-bar skeleton-field"></div>
            }
          }
          @case ('panel') {
            <div class="skeleton-bar skeleton-panel"></div>
          }
          @default {
            <div class="skeleton-bar skeleton-line"></div>
            <div class="skeleton-bar skeleton-line"></div>
            <div class="skeleton-bar skeleton-line skeleton-short"></div>
          }
        }
      </div>
      <span class="visually-hidden" role="status">{{ label() }}</span>
    }
    <div class="loading-content" [hidden]="hidden()"><ng-content /></div>
  `,
})
export class Loading implements OnDestroy {
  readonly loading = input(false);
  readonly shape = input<SkeletonShape>('lines');
  /** Read by assistive technology once the skeleton is visible. */
  readonly label = input('Loading');

  protected readonly rows = [1, 2, 3, 4, 5];
  protected readonly fieldRows = [1, 2, 3];
  protected readonly showing = signal(false);
  /** Content stays hidden while loading or while a shown skeleton is held. */
  protected readonly hidden = computed(() => this.loading() || this.showing());

  private delayTimer: ReturnType<typeof setTimeout> | null = null;
  private holdTimer: ReturnType<typeof setTimeout> | null = null;
  private shownAt = 0;

  constructor() {
    effect(() => {
      if (this.loading()) {
        this.start();
      } else {
        this.stop();
      }
    });
  }

  ngOnDestroy(): void {
    this.clear();
  }

  private start(): void {
    this.clear();
    if (this.showing()) {
      return;
    }
    this.delayTimer = setTimeout(() => {
      this.delayTimer = null;
      this.shownAt = Date.now();
      this.showing.set(true);
    }, SKELETON_DELAY_MS);
  }

  private stop(): void {
    this.clear();
    if (!this.showing()) {
      return;
    }
    const remaining = SKELETON_MIN_MS - (Date.now() - this.shownAt);
    if (remaining <= 0) {
      this.showing.set(false);
      return;
    }
    this.holdTimer = setTimeout(() => {
      this.holdTimer = null;
      this.showing.set(false);
    }, remaining);
  }

  private clear(): void {
    if (this.delayTimer !== null) {
      clearTimeout(this.delayTimer);
      this.delayTimer = null;
    }
    if (this.holdTimer !== null) {
      clearTimeout(this.holdTimer);
      this.holdTimer = null;
    }
  }
}
