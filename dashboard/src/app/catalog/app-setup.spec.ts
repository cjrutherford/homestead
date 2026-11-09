import { afterEach, describe, expect, it, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Subject } from 'rxjs';
import { ApiService, AppSetup as AppSetupView, OperationState } from '../core/api.service';
import { AppSetup } from './app-setup';

const URL = '/api/v1/catalog/gridcraft/setup';

function opState(id: number, status: string): OperationState {
  return { id, type: 'app.setup', status, steps: [], error: '' } as unknown as OperationState;
}

const STOPPED: AppSetupView = {
  app: 'gridcraft',
  status: 'stopped',
  install_operation: 7,
  operation: 60,
  reason: 'Stopped at the sign-in with Authentik: the plan changed after it was approved',
  steps: [
    { name: 'address', state: 'done', digest: 'ad1' },
    { name: 'sign-in', state: 'changed', digest: 'sd1', reason: 'the plan changed after it was approved' },
    { name: 'people', state: 'waiting', digest: 'pp1' },
  ],
};

const SIGN_IN_PLAN = {
  package: 'gridcraft', title: 'GridCraft', enabled: true, adapter: 'immich', provider: 'Homestead: GridCraft', application: 'GridCraft',
  client_id: 'homestead-gridcraft', issuer: 'x', redirect_uris: [], app_settings: [], changes: ['Update the redirect address'], warnings: [], digest: 'sd2',
};

describe('AppSetup', () => {
  let fixture: ComponentFixture<AppSetup>;
  let http: HttpTestingController;
  let streams: Map<number, Subject<OperationState>>;
  let settled: AppSetupView[];

  const text = () => (fixture.nativeElement as HTMLElement).textContent!.replace(/\s+/g, ' ');
  const button = (label: string) =>
    Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('button')).find((b) => b.textContent!.trim() === label) as
      | HTMLButtonElement
      | undefined;

  function open(view: Partial<AppSetupView>) {
    TestBed.configureTestingModule({ providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()] });
    streams = new Map();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockImplementation((id: number) => {
      const subject = new Subject<OperationState>();
      streams.set(id, subject);
      return subject;
    });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(AppSetup);
    fixture.componentRef.setInput('name', 'gridcraft');
    fixture.componentRef.setInput('title', 'GridCraft');
    settled = [];
    fixture.componentInstance.settled.subscribe((setup) => settled.push(setup));
    fixture.detectChanges();
    read(view);
  }

  function read(view: Partial<AppSetupView>) {
    http.expectOne(URL).flush({ app: 'gridcraft', steps: [], ...view });
    fixture.detectChanges();
  }

  afterEach(() => http.verify());

  it('shows nothing for an app with no setup, and says so', () => {
    open({ status: 'none' });
    expect(text().trim()).toBe('');
    expect(settled.map((s) => s.status)).toEqual(['none']);
  });

  it('follows a running setup and reads it again when its operation ends', () => {
    open({ status: 'running', operation: 60, steps: [{ name: 'address', state: 'done', digest: 'ad1' }, { name: 'publish', state: 'running', digest: 'pd1' }] });
    expect(text()).toContain('Setting up GridCraft');
    expect(text()).toContain('You can close this page; the setup keeps going.');
    expect(text()).toContain('Address on your network Done');
    expect(text()).toContain('Public address Running');
    expect(settled).toEqual([]);
    streams.get(60)!.next(opState(60, 'running'));
    http.expectNone(URL);
    streams.get(60)!.next(opState(60, 'succeeded'));
    read({ status: 'done', operation: 60, steps: [{ name: 'address', state: 'done', digest: 'ad1' }, { name: 'publish', state: 'done', digest: 'pd1' }] });
    expect(text()).toContain('GridCraft is set up');
    expect(settled.map((s) => s.status)).toEqual(['done']);
  });

  it('says where a setup stopped and offers Review and Skip the rest', () => {
    open(STOPPED);
    expect(text()).toContain("GridCraft's setup stopped");
    expect(text()).toContain('Sign-in with Authentik Plan changed');
    expect(text()).toContain('People who can sign in Waiting');
    expect(text()).toContain('Stopped at the sign-in with Authentik');
    expect(button('Review')).toBeDefined();
    expect(button('Skip the rest')).toBeDefined();
    expect(settled.map((s) => s.status)).toEqual(['stopped']);
  });

  it('reviews the current plan and resumes with its digest and a token used once', () => {
    open(STOPPED);
    button('Review')!.click();
    read({ ...STOPPED, review: { step: 'sign-in', digest: 'sd2', sign_in: SIGN_IN_PLAN, token_kept: false } as AppSetupView['review'] });
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('.permit-seal')?.getAttribute('aria-label')).toBe('Plan digest sd2');
    expect(text()).toContain('Update the redirect address');
    expect(button('Approve and resume')!.disabled).toBe(true);
    const token = el.querySelector('input[name="setup-token"]') as HTMLInputElement;
    token.value = 'ak-token';
    token.dispatchEvent(new Event('input'));
    fixture.detectChanges();
    button('Approve and resume')!.click();
    const resume = http.expectOne(`${URL}/resume`);
    expect(resume.request.body).toEqual({ digest: 'sd2', token: 'ak-token' });
    resume.flush({ id: 62 });
    fixture.detectChanges();
    expect((fixture.componentInstance as unknown as { token: string }).token).toBe('');
    streams.get(62)!.next(opState(62, 'succeeded'));
    read({ status: 'done', operation: 62, steps: [] });
    expect(text()).toContain('GridCraft is set up');
  });

  it('shows the newer plan when the plan moved again before the resume', () => {
    open(STOPPED);
    button('Review')!.click();
    read({ ...STOPPED, review: { step: 'sign-in', digest: 'sd2', sign_in: SIGN_IN_PLAN, token_kept: true } as AppSetupView['review'] });
    expect((fixture.nativeElement as HTMLElement).querySelector('input[name="setup-token"]')).toBeNull();
    button('Approve and resume')!.click();
    http.expectOne(`${URL}/resume`).flush('the plan changed since it was reviewed; review it again', { status: 409, statusText: 'Conflict' });
    read({ ...STOPPED, review: { step: 'sign-in', digest: 'sd3', sign_in: { ...SIGN_IN_PLAN, digest: 'sd3' }, token_kept: true } as AppSetupView['review'] });
    expect(text()).toContain('the plan changed since it was reviewed');
    expect((fixture.nativeElement as HTMLElement).querySelector('.permit-seal')?.getAttribute('aria-label')).toBe('Plan digest sd3');
  });

  it('offers only Skip the rest when the stopped step cannot be planned', () => {
    open(STOPPED);
    button('Review')!.click();
    read({ ...STOPPED, review: { step: 'sign-in', problem: 'Authentik is not installed.' } as AppSetupView['review'] });
    expect(text()).toContain('Authentik is not installed.');
    expect(button('Approve and resume')).toBeUndefined();
    expect(button('Skip the rest')).toBeDefined();
  });

  it('reviews a stopped Nextcloud link and resumes it (S326)', () => {
    open({
      ...STOPPED,
      reason: 'Stopped at the link to Nextcloud for files',
      steps: [
        { name: 'address', state: 'done', digest: 'ad1' },
        { name: 'nextcloud-link', state: 'failed', digest: 'fl1', reason: 'Nextcloud rejected the credential' },
      ],
    } as AppSetupView);
    expect(text()).toContain('Link to Nextcloud for files Failed');
    button('Review')!.click();
    read({
      ...STOPPED,
      review: {
        step: 'nextcloud-link', digest: 'fl1',
        files_link: { apps: ['gridcraft'], lines: ['Link GridCraft to Nextcloud for files'], base_url: 'https://homestead.lan/apps/nextcloud', account: 'admin', root: 'Homestead', new_link: true, digest: 'fl1' },
      },
    } as AppSetupView);
    expect(text()).toContain('Link GridCraft to Nextcloud for files');
    button('Approve and resume')!.click();
    const resume = http.expectOne(`${URL}/resume`);
    expect(resume.request.body).toEqual({ digest: 'fl1' });
    resume.flush({ id: 63 });
  });

  it('skips the rest of a stopped setup', () => {
    open(STOPPED);
    button('Skip the rest')!.click();
    const skip = http.expectOne(`${URL}/skip`);
    expect(skip.request.method).toBe('POST');
    skip.flush({
      ...STOPPED,
      status: 'skipped',
      steps: STOPPED.steps.map((step) => (step.state === 'done' ? step : { ...step, state: 'skipped' })),
    });
    fixture.detectChanges();
    expect(text()).toContain("GridCraft's setup was skipped");
    expect(text()).toContain('People who can sign in Skipped');
    expect(button('Review')).toBeUndefined();
    expect(settled.map((s) => s.status)).toEqual(['stopped', 'skipped']);
  });
});
