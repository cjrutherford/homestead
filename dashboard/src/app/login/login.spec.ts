import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Login } from './login';
import { SESSION_STORAGE_KEY } from '../auth/session.service';

describe('Login', () => {
  let fixture: ComponentFixture<Login>;
  let http: HttpTestingController;

  beforeEach(async () => {
    localStorage.clear();
    await TestBed.configureTestingModule({
      imports: [Login],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Login);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    http.verify();
    localStorage.clear();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('logs in and navigates to status', () => {
    const navigate = vi
      .spyOn(TestBed.inject(Router), 'navigateByUrl')
      .mockResolvedValue(true);
    fixture.componentInstance['submit']('owner', 'long-enough-password');
    const req = http.expectOne('/api/v1/sessions');
    expect(req.request.method).toBe('POST');
    req.flush({ token: 'session-token', expires_at: '2026-01-01T00:00:00Z' });
    expect(navigate).toHaveBeenCalledWith('/status');
    fixture.detectChanges();
    expect(localStorage.getItem(SESSION_STORAGE_KEY)).not.toContain('session-token');
    expect(localStorage.getItem(SESSION_STORAGE_KEY)).toContain('owner');
    expect(text()).toContain('Already logged in as');
  });

  it('reports bad credentials', () => {
    fixture.componentInstance['submit']('owner', 'wrong-password-here');
    const req = http.expectOne('/api/v1/sessions');
    req.flush('nope', { status: 401, statusText: 'Unauthorized' });
    fixture.detectChanges();
    expect(text()).toContain('Invalid username or password');
  });

  it('explains a lockout on 429', () => {
    fixture.componentInstance['submit']('owner', 'wrong-password-here');
    http
      .expectOne('/api/v1/sessions')
      .flush('account temporarily locked after repeated failures', {
        status: 429,
        statusText: 'Too Many Requests',
      });
    fixture.detectChanges();
    expect(text()).toContain('temporarily locked');
    expect(text()).not.toContain('reachable');
  });

  it('says the appliance is unreachable on status 0', () => {
    fixture.componentInstance['submit']('owner', 'wrong-password-here');
    http.expectOne('/api/v1/sessions').error(new ProgressEvent('error'), { status: 0 });
    fixture.detectChanges();
    expect(text()).toContain("Can't reach the appliance");
  });

  it('requires both fields without calling the API', () => {
    fixture.componentInstance['submit']('', '');
    http.expectNone('/api/v1/sessions');
    fixture.detectChanges();
    expect(text()).toContain('required');
  });
});
