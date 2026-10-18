import { describe, expect, it } from 'vitest';
import { ViewportScroller } from '@angular/common';
import { ApplicationInitStatus, ApplicationRef, Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { Router, provideRouter, withInMemoryScrolling } from '@angular/router';
import { SCROLL_OPTIONS } from './app.config';

@Component({ template: '' })
class Page {}

@Component({ selector: 'app-scroll-root', template: '' })
class Root {}

describe('router scrolling', () => {
  it('returns to the top on navigation and restores on back', async () => {
    const calls: Array<[number, number]> = [];
    TestBed.configureTestingModule({
      providers: [
        provideRouter(
          [
            { path: 'a', component: Page },
            { path: 'b', component: Page },
          ],
          withInMemoryScrolling(SCROLL_OPTIONS),
        ),
        {
          provide: ViewportScroller,
          useValue: {
            getScrollPosition: () => [0, 900],
            scrollToPosition: (position: [number, number]) => calls.push(position),
            setHistoryScrollRestoration: () => undefined,
            scrollToAnchor: () => undefined,
            setOffset: () => undefined,
          },
        },
      ],
    });
    await TestBed.inject(ApplicationInitStatus).donePromise;
    const router = TestBed.inject(Router);
    const host = document.createElement('app-scroll-root');
    document.body.appendChild(host);
    TestBed.inject(ApplicationRef).bootstrap(Root, host);
    await router.navigateByUrl('/a');
    await router.navigateByUrl('/b');
    await new Promise((resolve) => setTimeout(resolve));
    expect(SCROLL_OPTIONS.scrollPositionRestoration).toBe('enabled');
    expect(calls.length).toBeGreaterThan(0);
    expect(calls.every(([x, y]) => x === 0 && y === 0)).toBe(true);
  });
});
