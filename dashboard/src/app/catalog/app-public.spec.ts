import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { provideRouter } from '@angular/router';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { ApiService, AppPublicPlan, AppPublicStatus, OperationState } from '../core/api.service';
import { AppPublic } from './app-public';

function status(overrides: Partial<AppPublicStatus> = {}): AppPublicStatus {
  return {
    app: 'jellyfin', available: true, tunnel: 'homestead', tunnel_id: 'tid',
    routes: [{ route: 'jellyfin.lan', public: [] }], proposed: 'jellyfin.example.com', sign_in: false, ...overrides,
  };
}

const PLAN: AppPublicPlan = {
  app: 'jellyfin', action: 'add', hostname: 'watch.example.com', route: 'jellyfin.lan', tunnel: 'homestead', tunnel_id: 'tid',
  zone: 'example.com',
  dns: [{ name: 'watch.example.com', type: 'CNAME', content: 'tid.cfargotunnel.com', proxied: true, action: 'create', replaces: [] }],
  connector_before: ['auth.example.com'], connector_after: ['auth.example.com', 'watch.example.com'],
  warnings: ['Anyone on the internet can reach Jellyfin at https://watch.example.com.'], digest: 'p1',
};

describe('AppPublic (S290)', () => {
  let fixture: ComponentFixture<AppPublic>;
  let http: HttpTestingController;
  let stream: Subject<OperationState>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AppPublic],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    stream = new Subject<OperationState>();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockReturnValue(stream);
    fixture = TestBed.createComponent(AppPublic);
    fixture.componentRef.setInput('name', 'jellyfin');
    fixture.componentRef.setInput('title', 'Jellyfin');
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  const el = () => fixture.nativeElement as HTMLElement;
  const text = () => (el().textContent ?? '').replace(/\s+/g, ' ');
  const button = (label: string) =>
    Array.from(el().querySelectorAll('button')).find((b) => b.textContent?.trim() === label);

  function load(s: AppPublicStatus) {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog/jellyfin/public').flush(s);
    fixture.detectChanges();
  }

  it('reviews a named public address, warns, and approves exactly the plan', () => {
    load(status());
    expect(text()).toContain('reachable only on your network');
    const input = el().querySelector('input[name="public-hostname"]') as HTMLInputElement;
    expect(input.placeholder).toBe('jellyfin.example.com');
    input.value = 'watch.example.com';
    input.dispatchEvent(new Event('input'));
    button('Review public address')!.click();
    const plan = http.expectOne('/api/v1/catalog/jellyfin/public/plan');
    expect(plan.request.body).toEqual({ enabled: true, hostname: 'watch.example.com' });
    plan.flush(PLAN);
    fixture.detectChanges();
    expect(text()).toContain('Publish watch.example.com to jellyfin.lan through the tunnel homestead');
    expect(text()).toContain('Anyone on the internet');
    expect(text()).toContain('auth.example.com, watch.example.com');
    button('Approve')!.click();
    const apply = http.expectOne('/api/v1/catalog/jellyfin/public/apply');
    expect(apply.request.body).toEqual({ enabled: true, hostname: 'watch.example.com', route: 'jellyfin.lan', digest: 'p1' });
    apply.flush({ id: 12 });
    stream.next({ id: 12, status: 'succeeded' } as OperationState);
    http.expectOne('/api/v1/catalog/jellyfin/public').flush(status({ routes: [{ route: 'jellyfin.lan', public: ['watch.example.com'] }] }));
    fixture.detectChanges();
    expect(text()).toContain('Jellyfin is public at https://watch.example.com.');
    expect(el().querySelector('a[href="https://watch.example.com"]')).not.toBeNull();
  });

  it('shows a certificate lamp per public name and says the one-level rule (S312)', () => {
    load(
      status({
        routes: [{ route: 'jellyfin.lan', public: ['watch.example.com', 'watch.homestead.example.com'] }],
        coverage: [
          { hostname: 'watch.example.com', covered: true, by: 'universal', checked: true },
          { hostname: 'watch.homestead.example.com', covered: false, checked: true },
        ],
      }),
    );
    const lamps = Array.from(el().querySelectorAll('ul[aria-label="Public addresses"] .badge')).map((b) => [b.textContent?.trim(), b.className]);
    expect(lamps[0][0]).toBe('certificate covers it');
    expect(lamps[0][1]).toContain('badge-ok');
    expect(lamps[1][0]).toBe('no certificate yet');
    expect(lamps[1][1]).toContain('badge-warn');
    expect(text()).toContain('need Advanced Certificate Manager');
  });

  it('removes a public address and reads a stale plan', () => {
    load(status({ routes: [{ route: 'jellyfin.lan', public: ['watch.example.com'] }] }));
    button('Remove')!.click();
    const plan = http.expectOne('/api/v1/catalog/jellyfin/public/plan');
    expect(plan.request.body).toEqual({ enabled: false, hostname: 'watch.example.com' });
    plan.flush({ ...PLAN, action: 'remove', connector_after: ['auth.example.com'], digest: 'p2' });
    fixture.detectChanges();
    expect(text()).toContain('Remove watch.example.com from the tunnel homestead');
    button('Approve')!.click();
    const apply = http.expectOne('/api/v1/catalog/jellyfin/public/apply');
    expect(apply.request.body).toEqual({ enabled: false, hostname: 'watch.example.com', digest: 'p2' });
    apply.flush('the plan changed since it was reviewed; review it again', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(text()).toContain('review it again');
    expect(button('Approve')).toBeUndefined();
  });

  it('says why a public address is not available', () => {
    load(status({ available: false, reason: 'No tunnel is set up yet. Set one up, or adopt yours, on the Tunnel page.' }));
    expect(text()).toContain('not available: No tunnel is set up yet');
    expect(el().querySelector('a[href="/tunnel"]')).not.toBeNull();
    expect(button('Review public address')).toBeUndefined();
  });

  it('publishes a path app at its path under the dashboard\'s name, with no name to type (S315)', () => {
    load(status({
      routes: [{ route: 'jellyfin.lan', public: [], path: '/apps/jellyfin/' }],
      proposed: 'homestead.example.com', proposed_url: 'https://homestead.example.com/apps/jellyfin/',
    }));
    expect(el().querySelector('input[name="public-hostname"]')).toBeNull();
    expect(text()).toContain('answers at /apps/jellyfin/ under the dashboard\'s public name');
    button('Review publishing at https://homestead.example.com/apps/jellyfin/')!.click();
    const plan = http.expectOne('/api/v1/catalog/jellyfin/public/plan');
    expect(plan.request.body).toEqual({ enabled: true });
    plan.flush({
      ...PLAN, hostname: 'homestead.example.com', path: '/apps/jellyfin/', url: 'https://homestead.example.com/apps/jellyfin/',
      dns: [], connector_before: ['homestead.example.com'], connector_after: ['homestead.example.com'], digest: 'pp1',
    });
    fixture.detectChanges();
    expect(text()).toContain('Publish https://homestead.example.com/apps/jellyfin/ through the tunnel homestead');
    expect(text()).toContain('no DNS record changes');
    button('Approve')!.click();
    const apply = http.expectOne('/api/v1/catalog/jellyfin/public/apply');
    expect(apply.request.body).toEqual({ enabled: true, hostname: 'homestead.example.com', route: 'jellyfin.lan', digest: 'pp1' });
    apply.flush({ id: 13 });
    stream.next({ id: 13, status: 'succeeded' } as OperationState);
    http.expectOne('/api/v1/catalog/jellyfin/public').flush(status({
      routes: [{ route: 'jellyfin.lan', public: ['homestead.example.com'], path: '/apps/jellyfin/', url: 'https://homestead.example.com/apps/jellyfin/' }],
    }));
    fixture.detectChanges();
    expect(text()).toContain('Jellyfin is public at https://homestead.example.com/apps/jellyfin/.');
    expect(el().querySelector('a[href="https://homestead.example.com/apps/jellyfin/"]')).not.toBeNull();
  });
});
