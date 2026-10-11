import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
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

  it('skips to the admin step when claimed without progress', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    answerWizard(null);
    expect(text()).toContain('Create the first administrator');
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
    flushSave('done');
    expect(text()).toContain('administrator created');
    expect(text()).toContain('Step 3 of 3');
    // The owner is logged in locally by setup.
    expect(TestBed.inject(SessionService).username()).toBe('owner');
  });

  it('lands on done when the administrator exists', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    answerWizard(null);
    submitAdmin('owner', 'long-enough-password', 'long-enough-password');
    const req = http.expectOne('/api/v1/admins/setup');
    req.flush('exists', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(text()).toContain('already exists');
  });

  it('rejects mismatched passwords without calling the API', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    answerWizard(null);
    submitAdmin('owner', 'long-enough-password', 'different-password');
    http.expectNone('/api/v1/admins/setup');
    fixture.detectChanges();
    expect(text()).toContain('do not match');
  });
});
