import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { ApiService, OperationState } from '../core/api.service';
import { workOrderInterceptor } from '../core/work-order.interceptor';
import { WorkOrders } from '../core/work-orders.service';
import { UninstallApp } from './uninstall-app';

@Component({
  imports: [UninstallApp],
  template: `<app-uninstall-app [name]="name" [title]="title" (uninstalled)="gone = gone + 1" />`,
})
class Host {
  name = 'nextcloud';
  title = 'Nextcloud';
  gone = 0;
}

const MANIFEST = {
  Name: 'nextcloud',
  Title: 'Nextcloud',
  Services: [
    {
      Name: 'nextcloud',
      Image: 'nextcloud:35',
      Exposes: 'web',
      Data: [
        { Target: '/a', Class: 'application', Subpath: 'html' },
        { Target: '/b', Class: 'bulk', Subpath: 'data' },
      ],
    },
  ],
  Requirements: { MemoryMB: 1, Storage: [{ Class: 'application', MinBytes: 1 }], Ports: [] },
  Secrets: [{ Name: 'db_password', Immutable: false }],
};
const ASSIGNMENTS = [
  { Class: 'application', Mountpoint: '/mnt/app', Actor: 'o', CreatedAt: '' },
  { Class: 'bulk', Mountpoint: '/mnt/bulk', Actor: 'o', CreatedAt: '' },
];

function state(id: number, status: string): OperationState {
  return { id, type: 'catalog.remove', status, payload: '', result: '', error: '', created_at: new Date().toISOString(), updated_at: '', steps: [
    { id: 1, operation_id: id, position: 0, name: 'down', status: 'running', started_at: '', finished_at: '', error: '' },
  ] };
}

describe('UninstallApp', () => {
  let fixture: ComponentFixture<Host>;
  let http: HttpTestingController;
  let stream: Subject<OperationState>;

  const text = () => (fixture.nativeElement as HTMLElement).textContent!.replace(/\s+/g, ' ');
  const dialog = () => (fixture.nativeElement as HTMLElement).querySelector('dialog');
  const find = (label: string) =>
    Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('button')).find(
      (b) => b.textContent!.trim() === label,
    ) as HTMLButtonElement;

  beforeEach(() => {
    HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) { this.setAttribute('open', ''); };
    TestBed.configureTestingModule({
      imports: [Host],
      providers: [provideRouter([]), provideHttpClient(withInterceptors([workOrderInterceptor])), provideHttpClientTesting()],
    });
    stream = new Subject();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockReturnValue(stream);
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
  });

  afterEach(() => http.verify());

  function open() {
    find('Uninstall Nextcloud').click();
    http.expectOne('/api/v1/catalog/nextcloud').flush(MANIFEST);
    http.expectOne('/api/v1/storage/assignments').flush(ASSIGNMENTS);
    fixture.detectChanges();
  }

  it('asks first, in plain words, and keeps the data by default', () => {
    expect(dialog()).toBeNull();
    open();
    expect(dialog()).not.toBeNull();
    expect(text()).toContain('Uninstall Nextcloud?');
    expect(text()).toContain('removes its containers, its network and the files Homestead generated for it');
    expect(text()).toContain('Its data is kept unless you choose to delete it.');
    expect(text()).toContain('/mnt/app/nextcloud');
    expect(text()).toContain('/mnt/bulk/nextcloud');
    expect(text()).toContain('stay in the secret store');
    expect(text()).not.toContain('permanently deletes');
  });

  it('cancels without a request', () => {
    open();
    find('Keep Nextcloud').click();
    fixture.detectChanges();
    expect(dialog()).toBeNull();
    http.expectNone('/api/v1/catalog/nextcloud/uninstall');
  });

  it('uninstalls keeping data, joins the rail and reports the result', () => {
    open();
    find('Confirm uninstall').click();
    const request = http.expectOne('/api/v1/catalog/nextcloud/uninstall');
    expect(request.request.body).toEqual({});
    request.flush({ id: 21 });
    fixture.detectChanges();
    expect(TestBed.inject(WorkOrders).orders()[0].title).toBe('Uninstall Nextcloud');
    expect(TestBed.inject(WorkOrders).orders()[0].done).toBe('Nextcloud uninstalled');
    expect((fixture.nativeElement as HTMLElement).querySelector('app-operation-progress a[href="/operations/21"]')).not.toBeNull();
    expect(find('Uninstall Nextcloud')).toBeUndefined();
    stream.next(state(21, 'succeeded'));
    fixture.detectChanges();
    expect(fixture.componentInstance.gone).toBe(1);
    expect(text()).toContain('Nextcloud uninstalled');
    expect(text()).toContain('Its data was kept.');
  });

  it('deletes data only when asked, naming every root explicitly', () => {
    open();
    const box = (fixture.nativeElement as HTMLElement).querySelector('input[type="checkbox"]') as HTMLInputElement;
    box.click();
    fixture.detectChanges();
    expect(text()).toContain('permanently deletes everything in the folders above');
    find('Confirm uninstall').click();
    const request = http.expectOne('/api/v1/catalog/nextcloud/uninstall');
    expect(request.request.body).toEqual({
      remove_data: true,
      data_dirs: [
        { mountpoint: '/mnt/app', path: 'nextcloud' },
        { mountpoint: '/mnt/bulk', path: 'nextcloud' },
      ],
    });
    request.flush({ id: 22 });
    fixture.detectChanges();
    stream.next(state(22, 'succeeded'));
    fixture.detectChanges();
    expect(text()).toContain('Its data was deleted.');
  });

  it('explains a refusal for data in Docker volumes and offers to leave it behind', () => {
    open();
    find('Confirm uninstall').click();
    http
      .expectOne('/api/v1/catalog/nextcloud/uninstall')
      .flush('"nextcloud" keeps data in Docker volumes outside mounted storage; uninstalling would orphan it.', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(dialog()).not.toBeNull();
    expect(text()).toContain('keeps data in Docker volumes');
    expect(text()).toContain('Nothing was removed.');
    expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/data"]')).not.toBeNull();
    const abandon = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('input[type="checkbox"]')).pop() as HTMLInputElement;
    abandon.click();
    fixture.detectChanges();
    find('Confirm uninstall').click();
    const retry = http.expectOne('/api/v1/catalog/nextcloud/uninstall');
    expect(retry.request.body.abandon_volumes).toBe(true);
    retry.flush({ id: 23 });
  });

  it('keeps data when Homestead cannot tell where it lives', () => {
    find('Uninstall Nextcloud').click();
    http.expectOne('/api/v1/catalog/nextcloud').flush(MANIFEST);
    http.expectOne('/api/v1/storage/assignments').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('Homestead cannot tell which folders hold it');
    expect((fixture.nativeElement as HTMLElement).querySelector('input[type="checkbox"]')).toBeNull();
  });

  it('says there is no data for an app that keeps nothing', () => {
    find('Uninstall Nextcloud').click();
    http.expectOne('/api/v1/catalog/nextcloud').flush({
      ...MANIFEST,
      Services: [{ Name: 'nextcloud', Image: 'homestead/craft-x:1', Exposes: 'web', Data: [{ Target: '/tmp', Class: '', Subpath: '', Ephemeral: true }] }],
      Requirements: { MemoryMB: 1, Storage: null, Ports: null },
      Secrets: [],
    });
    http.expectOne('/api/v1/storage/assignments').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('keeps nothing on the appliance, so there is no data to keep or delete');
  });

  it('tells the owner when the appliance cannot be reached', () => {
    find('Uninstall Nextcloud').click();
    http.expectOne('/api/v1/catalog/nextcloud').error(new ProgressEvent('error'), { status: 0 });
    // The assignments request is cancelled with the failed one.
    expect(http.match('/api/v1/storage/assignments')).toHaveLength(1);
    fixture.detectChanges();
    expect(text()).toContain('Cannot reach the appliance, so Nextcloud was not uninstalled.');
    expect(dialog()).toBeNull();
  });
});
