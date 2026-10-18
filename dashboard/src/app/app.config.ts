import { ApplicationConfig, provideBrowserGlobalErrorListeners } from '@angular/core';
import {
  InMemoryScrollingOptions,
  provideRouter,
  withInMemoryScrolling,
  withViewTransitions,
} from '@angular/router';
import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { authInterceptor } from './core/auth.interceptor';
import { routes } from './app.routes';
import { workOrderInterceptor } from './core/work-order.interceptor';
import { viewTransitionOptions } from './core/view-transition';

/**
 * A new page starts at the top; back and forward restore the earlier
 * position. Without this the document keeps its old scroll offset across
 * route changes and a short-lived tall page leaves the next one scrolled to
 * its bottom.
 */
export const SCROLL_OPTIONS: InMemoryScrollingOptions = {
  scrollPositionRestoration: 'enabled',
  anchorScrolling: 'enabled',
};

export const appConfig: ApplicationConfig = {
  providers: [
    provideBrowserGlobalErrorListeners(),
    provideRouter(routes, withInMemoryScrolling(SCROLL_OPTIONS), withViewTransitions(viewTransitionOptions)),
    provideHttpClient(withInterceptors([authInterceptor, workOrderInterceptor]))
  ]
};
