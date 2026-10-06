import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Claim } from './claim';

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

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('starts on the token step when unclaimed', () => {
    fixture.detectChanges();
    answerIdentity('UNCLAIMED');
    expect(text()).toContain('Claim token');
  });

  it('skips to the admin step when already claimed', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    expect(text()).toContain('Create the first administrator');
  });

  it('advances to admin after a valid token', () => {
    fixture.detectChanges();
    answerIdentity('UNCLAIMED');
    fixture.componentInstance['submitToken']('good-token');
    const req = http.expectOne('/api/v1/claim');
    expect(req.request.method).toBe('POST');
    req.flush({ state: 'CLAIMED' });
    fixture.detectChanges();
    expect(text()).toContain('Create the first administrator');
  });

  it('reports an invalid token', () => {
    fixture.detectChanges();
    answerIdentity('UNCLAIMED');
    fixture.componentInstance['submitToken']('bad-token');
    const req = http.expectOne('/api/v1/claim');
    req.flush('invalid or expired', { status: 401, statusText: 'Unauthorized' });
    fixture.detectChanges();
    expect(text()).toContain('Invalid or expired token');
  });

  it('completes after creating the administrator', () => {
    fixture.detectChanges();
    answerIdentity('UNCLAIMED');
    fixture.componentInstance['submitToken']('good-token');
    http.expectOne('/api/v1/claim').flush({ state: 'CLAIMED' });
    fixture.detectChanges();
    fixture.componentInstance['submitAdmin']('owner', 'long-enough-password', 'long-enough-password');
    const req = http.expectOne('/api/v1/admins/setup');
    expect(req.request.method).toBe('POST');
    req.flush({ id: 1, username: 'owner' });
    fixture.detectChanges();
    expect(text()).toContain('administrator created');
  });

  it('lands on done when the administrator exists', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    fixture.componentInstance['submitAdmin']('owner', 'long-enough-password', 'long-enough-password');
    const req = http.expectOne('/api/v1/admins/setup');
    req.flush('exists', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(text()).toContain('already exists');
  });

  it('rejects mismatched passwords without calling the API', () => {
    fixture.detectChanges();
    answerIdentity('CLAIMED');
    fixture.componentInstance['submitAdmin']('owner', 'long-enough-password', 'different-password');
    http.expectNone('/api/v1/admins/setup');
    fixture.detectChanges();
    expect(text()).toContain('do not match');
  });
});
