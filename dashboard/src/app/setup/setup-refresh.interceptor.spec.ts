import { HttpErrorResponse, HttpInterceptorFn, HttpRequest, HttpResponse } from '@angular/common/http';
import { TestBed } from '@angular/core/testing';
import { Observable, of, throwError } from 'rxjs';
import { setupRefreshInterceptor } from './setup-refresh.interceptor';
import { SetupService } from './setup.service';

describe('setupRefreshInterceptor', () => {
  let touch: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    touch = vi.fn();
    TestBed.configureTestingModule({ providers: [{ provide: SetupService, useValue: { touch } }] });
  });

  function run(method: string, url: string, outcome: 'ok' | 'fail' = 'ok'): void {
    const req = new HttpRequest(method as 'GET', url);
    const next = () =>
      (outcome === 'ok'
        ? of(new HttpResponse({ status: 200 }))
        : throwError(() => new HttpErrorResponse({ status: 500 }))) as Observable<HttpResponse<unknown>>;
    TestBed.runInInjectionContext(() =>
      (setupRefreshInterceptor as HttpInterceptorFn)(req, next).subscribe({ error: () => undefined }),
    );
  }

  it('touches the wizard after a successful write', () => {
    run('POST', '/api/v1/storage/assign');
    expect(touch).toHaveBeenCalledTimes(1);
  });

  it('ignores reads', () => {
    run('GET', '/api/v1/storage');
    expect(touch).not.toHaveBeenCalled();
  });

  it('ignores the setup routes themselves', () => {
    run('PUT', '/api/v1/setup/steps/storage');
    expect(touch).not.toHaveBeenCalled();
  });

  it('ignores a failed write', () => {
    run('POST', '/api/v1/storage/assign', 'fail');
    expect(touch).not.toHaveBeenCalled();
  });
});
