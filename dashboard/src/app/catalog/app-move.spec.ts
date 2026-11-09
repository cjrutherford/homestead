import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { provideRouter } from '@angular/router';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { ApiService, AppMovePlan, AppMoveStatus, OperationState } from '../core/api.service';
import { AppMove } from './app-move';

const OFFERED: AppMoveStatus = {
  app: 'nextcloud', offered: true, from: 'nextcloud.lan', path: '/apps/nextcloud/',
  to: ['https://homestead.lan/apps/nextcloud/', 'https://homestead.local/apps/nextcloud/'],
};

const PLAN: AppMovePlan = {
  app: 'nextcloud', route: 'nextcloud.lan', from: 'nextcloud.lan', path: '/apps/nextcloud/', to: OFFERED.to,
  strip_prefix: false, redirects: ['/.well-known/caldav -> /apps/nextcloud/remote.php/dav'],
  app_settings: ['OVERWRITEWEBROOT=/apps/nextcloud'], sign_in: ['Re-plan sign-in for Nextcloud after the move.'],
  warnings: ['Nextcloud stops answering at https://nextcloud.lan.'], digest: 'mv1',
};

describe('AppMove (S315)', () => {
  let fixture: ComponentFixture<AppMove>;
  let http: HttpTestingController;
  let stream: Subject<OperationState>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [AppMove],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    stream = new Subject<OperationState>();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockReturnValue(stream);
    fixture = TestBed.createComponent(AppMove);
    fixture.componentRef.setInput('name', 'nextcloud');
    fixture.componentRef.setInput('title', 'Nextcloud');
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  const el = () => fixture.nativeElement as HTMLElement;
  const text = () => (el().textContent ?? '').replace(/\s+/g, ' ');
  const button = (label: string) =>
    Array.from(el().querySelectorAll('button')).find((b) => b.textContent?.trim() === label);

  function load(s: AppMoveStatus) {
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog/nextcloud/move').flush(s);
    fixture.detectChanges();
  }

  it('previews the removal of the old public name with no redirect (S471, ADR-061)', () => {
    load({ ...OFFERED, public_from: 'https://nextcloud.example.com/', public_to: 'https://homestead.example.com/apps/nextcloud/' });
    expect(text()).toContain('Old bookmarks and links to it stop working; nothing redirects.');
    button('Review the move')!.click();
    http.expectOne('/api/v1/catalog/nextcloud/move/plan').flush({
      ...PLAN,
      public_from: 'https://nextcloud.example.com/', public_to: 'https://homestead.example.com/apps/nextcloud/',
      removes: ['DNS: the proxied CNAME nextcloud.example.com to the tunnel is deleted'], stops: ['https://nextcloud.example.com/'],
      warnings: ['Old bookmarks and links on https://nextcloud.example.com/ stop working.'],
    });
    fixture.detectChanges();
    const preview = text();
    expect(preview).toContain('https://nextcloud.example.com/ is removed');
    expect(preview).toContain('the proxied CNAME nextcloud.example.com to the tunnel is deleted');
    expect(preview).toContain('Stops working, with no redirect: https://nextcloud.example.com/');
    expect(preview).toContain('Old bookmarks and links on https://nextcloud.example.com/ stop working.');
  });

  it('offers to finish a move that stopped before the old name was removed (S479)', () => {
    load({ app: 'nextcloud', offered: true, to: [], finish: true,
      public_from: 'https://nextcloud.example.com/', public_to: 'https://homestead.example.com/apps/nextcloud/' });
    expect(text()).toContain('Finish the move');
    expect(text()).toContain('removes the old name only if it answers');
    expect(button('Review finishing the move')).toBeDefined();
    expect(button('Review the move')).toBeUndefined();
  });

  it('shows nothing when the move is not offered', () => {
    load({ app: 'nextcloud', offered: false, reason: 'Nextcloud is already at /apps/nextcloud/.', to: [] });
    expect(el().querySelector('.app-move')).toBeNull();
    expect(button('Review the move')).toBeUndefined();
  });

  it('offers the move, shows the plan, and moves nothing until approved by digest', () => {
    load(OFFERED);
    expect(el().querySelector('#app-move-heading')?.textContent).toBe('Move to https://homestead.lan/apps/nextcloud/');
    button('Review the move')!.click();
    const plan = http.expectOne('/api/v1/catalog/nextcloud/move/plan');
    expect(plan.request.method).toBe('POST');
    plan.flush(PLAN);
    fixture.detectChanges();
    expect(text()).toContain('From nextcloud.lan to https://homestead.lan/apps/nextcloud/, https://homestead.local/apps/nextcloud/.');
    expect(text()).toContain('Nextcloud serves under it');
    expect(text()).toContain('OVERWRITEWEBROOT=/apps/nextcloud');
    expect(text()).toContain('Re-plan sign-in for Nextcloud');
    expect(text()).toContain('stops answering at https://nextcloud.lan');
    // Nothing was applied yet.
    http.expectNone('/api/v1/catalog/nextcloud/move/apply');
    const moved = vi.fn();
    fixture.componentInstance.moved.subscribe(moved);
    button('Approve the move')!.click();
    const apply = http.expectOne('/api/v1/catalog/nextcloud/move/apply');
    expect(apply.request.body).toEqual({ digest: 'mv1' });
    apply.flush({ id: 41 });
    fixture.detectChanges();
    expect(text()).toContain('Move Nextcloud from nextcloud.lan to');
    stream.next({ id: 41, status: 'succeeded' } as OperationState);
    http.expectOne('/api/v1/catalog/nextcloud/move').flush({ app: 'nextcloud', offered: false, reason: 'already moved', to: [] });
    fixture.detectChanges();
    expect(moved).toHaveBeenCalled();
    expect(text()).toContain('Nextcloud now answers at https://homestead.lan/apps/nextcloud/');
  });

  it('drops a plan that changed and asks for a fresh review', () => {
    load(OFFERED);
    button('Review the move')!.click();
    http.expectOne('/api/v1/catalog/nextcloud/move/plan').flush(PLAN);
    fixture.detectChanges();
    button('Approve the move')!.click();
    http.expectOne('/api/v1/catalog/nextcloud/move/apply').flush('the plan changed since it was reviewed', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(button('Approve the move')).toBeUndefined();
    expect(text()).toContain('the plan changed since it was reviewed');
    expect(button('Review the move')).toBeDefined();
  });
});
