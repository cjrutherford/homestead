import { HttpEvent, HttpInterceptorFn, HttpResponse } from '@angular/common/http';
import { Injector, inject } from '@angular/core';
import { tap } from 'rxjs';
import { labelForRequest } from './operation-labels';
import { WorkOrders } from './work-orders.service';

/**
 * Every operation the dashboard starts joins the work-orders rail (S283):
 * a successful POST or DELETE to a known operation-starting route whose body
 * carries the new operation id. Pages need no tracking code of their own.
 */
export const workOrderInterceptor: HttpInterceptorFn = (req, next) => {
  const injector = inject(Injector);
  const label = req.method === 'GET' ? null : labelForRequest(req.method, req.url);
  if (!label) {
    return next(req);
  }
  return next(req).pipe(
    tap((event: HttpEvent<unknown>) => {
      if (!(event instanceof HttpResponse)) {
        return;
      }
      const body = event.body as { id?: unknown } | null;
      if (body && typeof body.id === 'number' && body.id > 0) {
        injector.get(WorkOrders).track(body.id, label);
      }
    }),
  );
};
