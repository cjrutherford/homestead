import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Claim } from './claim';
import { SessionService } from '../auth/session.service';

describe('Claim', () => {
  let fixture: ComponentFixture<Claim>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Claim],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Claim);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  function answerIdentity(state: string): void {
    const req = http.expectOne('/api/v1/identity');
    req.flush({ state, version: 'test' });
    fixture.detectChanges();
  }

  function answerWizard(step: string | null): void {
    const req = http.expectOne('/api/v1/wizards/claim');
    if (step === null) {
      req.flush('none', { status: 404, statusText: 'Not Found' });
    } else {
      req.flush({ wizard: 'claim', step, payload: {}, updated_at: '' });
    }
    fixture.detectChanges();
  }

  function flushSave(expectedStep: string): void {
    const req = http.expectOne('/api/v1/wizards/claim');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({ step: expectedStep, payload: {} });
    req.flush({ wizard: 'claim', step: expectedStep, payload: {}, updated_at: '' });
    fixture.detectChanges();
  }

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  function submitToken(token: string): void {
    fixture.componentInstance['submitToken'](token);
  }

  function submitAdmin(name: string, password: string, confirm: string): void {
    fixture.componentInstance['submitAdmin'](name, password, confirm);
  }

  it('starts on the token step when unclaimed, ignoring saved progress', () => {
    fixture.detectChanges();
    answerIdentity('UNCLAIMED');
    http.expectNone('/api/v1/wizards/claim');
    expect(text()).toContain('Claim token');
  });

  function refuseWizard(status: number, body: string): void {
    http.expectOne('/api/v1/wizards/claim').flush(body, { status, statusText: 'x' });
    fixture.detectChanges();
  }

  it('shows already set up, not the admin form, when claimed and the wizard load is refused', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    refuseWizard(403, 'forbidden');
    expect(text()).toContain('already set up');
    expect(text()).not.toContain('Create the first administrator');
    expect(fixture.nativeElement.querySelector('form')).toBeNull();
    expect(fixture.nativeElement.querySelector('a[href="/login"]')).not.toBeNull();
  });

  it('shows already set up on the claim-window-closed 401', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    refuseWizard(401, 'session required');
    expect(text()).toContain('already set up');
    expect(fixture.nativeElement.querySelector('form')).toBeNull();
  });

  it('reports an unreachable appliance when the wizard load fails with 503', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    refuseWizard(503, 'down');
    expect(text()).toContain("Can't reach the appliance");
    expect(text()).not.toContain('Create the first administrator');
  });

  it('resumes the admin step from saved progress', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    answerWizard('admin');
    expect(text()).toContain('Create the first administrator');
    expect(text()).not.toContain('single-use claim token');
  });

  it('resumes done from saved progress', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    answerWizard('done');
    expect(text()).toContain('already exists');
  });

  it('advances to admin after a valid token without a wizard save (no session yet)', () => {
    fixture.detectChanges();
    answerIdentity('UNCLAIMED');
    submitToken('good-token');
    const req = http.expectOne('/api/v1/claim');
    expect(req.request.method).toBe('POST');
    req.flush({ state: 'CLAIMED' });
    fixture.detectChanges();
    expect(text()).toContain('Create the first administrator');
    expect(text()).toContain('Step 2 of 3');
  });

  it('resumes at the account step when claimed with no administrator yet (reload)', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/identity').flush({ state: 'CLAIMED', version: 'test', setup_pending: true });
    fixture.detectChanges();
    http.expectNone('/api/v1/wizards/claim');
    expect(text()).toContain('Create the first administrator');
    expect(text()).not.toContain('already set up');
  });

  it('reports an invalid token', () => {
    fixture.detectChanges();
    answerIdentity('UNCLAIMED');
    submitToken('bad-token');
    const req = http.expectOne('/api/v1/claim');
    req.flush('invalid or expired', { status: 401, statusText: 'Unauthorized' });
    fixture.detectChanges();
    expect(text()).toContain('Invalid or expired token');
  });

  it('completes after creating the administrator', () => {
    fixture.detectChanges();
    answerIdentity('UNCLAIMED');
    submitToken('good-token');
    http.expectOne('/api/v1/claim').flush({ state: 'CLAIMED' });
    fixture.detectChanges();
    submitAdmin('owner', 'long-enough-password', 'long-enough-password');
    const req = http.expectOne('/api/v1/admins/setup');
    expect(req.request.method).toBe('POST');
    req.flush({ id: 1, username: 'owner', expires_at: '2030-01-01T00:00:00Z' });
    fixture.detectChanges();
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    flushSave('done');
    expect(text()).toContain('administrator created');
    expect(text()).toContain('Step 3 of 3');
    // The owner is logged in locally by setup.
    expect(TestBed.inject(SessionService).username()).toBe('owner');
    // The owner just chose the password: setup's confirmation is recorded,
    // then the setup wizard opens (S292).
    const confirm = http.expectOne('/api/v1/setup/steps/account');
    expect(confirm.request.method).toBe('PUT');
    expect(confirm.request.body).toEqual({ state: 'done' });
    expect(navigate).not.toHaveBeenCalled();
    confirm.flush({});
    expect(navigate).toHaveBeenCalledWith(['/setup']);
  });

  it('still opens setup when recording the password confirmation fails', () => {
    fixture.detectChanges();
    answerIdentity('UNCLAIMED');
    submitToken('good-token');
    http.expectOne('/api/v1/claim').flush({ state: 'CLAIMED' });
    fixture.detectChanges();
    submitAdmin('owner', 'long-enough-password', 'long-enough-password');
    http.expectOne('/api/v1/admins/setup').flush({ id: 1, username: 'owner', expires_at: '2030-01-01T00:00:00Z' });
    fixture.detectChanges();
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    flushSave('done');
    http.expectOne('/api/v1/setup/steps/account').flush('no', { status: 500, statusText: 'x' });
    expect(navigate).toHaveBeenCalledWith(['/setup']);
  });

  it('lands on done when the administrator exists', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    answerWizard('admin');
    submitAdmin('owner', 'long-enough-password', 'long-enough-password');
    const req = http.expectOne('/api/v1/admins/setup');
    req.flush('exists', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(text()).toContain('already exists');
  });

  it('rejects mismatched passwords without calling the API', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    answerWizard('admin');
    submitAdmin('owner', 'long-enough-password', 'different-password');
    http.expectNone('/api/v1/admins/setup');
    fixture.detectChanges();
    expect(text()).toContain('do not match');
  });

  it('does not say the daemon is unreachable when setup is refused', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    answerWizard('admin');
    submitAdmin('owner', 'long-enough-password', 'long-enough-password');
    http
      .expectOne('/api/v1/admins/setup')
      .flush('appliance must be claimed first', { status: 403, statusText: 'Forbidden' });
    fixture.detectChanges();
    expect(text()).toContain('not been claimed');
    expect(text()).not.toContain('reachable');
  });
});
