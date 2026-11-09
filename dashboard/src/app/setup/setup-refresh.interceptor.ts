import { HttpEvent, HttpInterceptorFn, HttpResponse } from '@angular/common/http';
import { Injector, inject } from '@angular/core';
import { tap } from 'rxjs';
import { SetupService } from './setup.service';

/**
 * The setup wizard embeds the same pages as the standalone ones (S291), and
 * those pages know nothing about the wizard. Any successful write to the API
 * other than the setup routes themselves may change what the wizard's status
 * derives from, so it tells the wizard to read the status again.
 */
export const setupRefreshInterceptor: HttpInterceptorFn = (req, next) => {
  const injector = inject(Injector);
  const write = req.method !== 'GET' && req.method !== 'HEAD';
  if (!write || !req.url.startsWith('/api/') || req.url.startsWith('/api/v1/setup')) {
    return next(req);
  }
  return next(req).pipe(
    tap((event: HttpEvent<unknown>) => {
      if (event instanceof HttpResponse && event.ok) {
        injector.get(SetupService).touch();
      }
    }),
  );
};
