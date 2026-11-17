import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { provideRouter } from '@angular/router';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { ApiService, OperationState, ReadLinksPlan, ReadLinksStatus } from '../core/api.service';
import { ReadLinks } from './read-links';

const CLOSED: ReadLinksStatus = { app: 'tasks', title: 'Tasks', available: true, exposed: false, links: 2, sign_in: true };
const PLAN: ReadLinksPlan = {
  app: 'tasks', title: 'Tasks', action: 'expose', origin: 'https://home.example.org', route: 'home.lan',
  patterns: ['/apps/tasks/_homestead/read/<token>', '/apps/tasks/_homestead/view/<token>'],
  refused: ['the app itself'], links: 2, expiry_cap_days: 365, warnings: ['Anyone with a link can read.'], digest: 'rl1',
};

describe('ReadLinks (S433)', () => {
  let fixture: ComponentFixture<ReadLinks>;
  let http: HttpTestingController;
  let stream: Subject<OperationState>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ReadLinks],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    stream = new Subject<OperationState>();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockReturnValue(stream);
    fixture = TestBed.createComponent(ReadLinks);
    fixture.componentRef.setInput('name', 'tasks');
    fixture.componentRef.setInput('title', 'Tasks');
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  const el = () => fixture.nativeElement as HTMLElement;
  const text = () => (el().textContent ?? '').replace(/\s+/g, ' ');
  const button = (label: string) => Array.from(el().querySelectorAll('button')).find((b) => b.textContent?.trim() === label);

  function load(s: ReadLinksStatus) {
    fixture.detectChanges();
    http.expectOne('/api/v1/apps/tasks/read-links').flush(s);
    fixture.detectChanges();
  }

  it('shows nothing for an app without links', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/apps/tasks/read-links').flush('no', { status: 404, statusText: 'Not Found' });
    fixture.detectChanges();
    expect(el().querySelector('.read-links')).toBeNull();
  });

  it('plans first and opens the paths only when approved by digest', () => {
    load(CLOSED);
    button('Review opening public links')!.click();
    http.expectOne('/api/v1/apps/tasks/read-links/plan').flush(PLAN);
    fixture.detectChanges();
    expect(text()).toContain('/apps/tasks/_homestead/read/<token>');
    expect(text()).toContain('Stays closed: the app itself');
    button('Approve')!.click();
    const req = http.expectOne('/api/v1/apps/tasks/read-links/approve');
    expect(req.request.body).toEqual({ enabled: true, digest: 'rl1' });
    req.flush({ id: 7 });
    fixture.detectChanges();
    stream.next({ status: 'succeeded' } as OperationState);
    http.expectOne('/api/v1/apps/tasks/read-links').flush({ ...CLOSED, exposed: true, origin: 'https://home.example.org' });
    fixture.detectChanges();
    expect(text()).toContain('open on https://home.example.org');
  });

  it('sends nothing on cancel', () => {
    load(CLOSED);
    button('Review opening public links')!.click();
    http.expectOne('/api/v1/apps/tasks/read-links/plan').flush(PLAN);
    fixture.detectChanges();
    button('Cancel')!.click();
    fixture.detectChanges();
    expect(button('Review opening public links')).toBeDefined();
  });
});
