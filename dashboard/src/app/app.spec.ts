import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { App } from './app';
import { routes } from './app.routes';
import { SESSION_STORAGE_KEY } from './auth/session.service';

describe('App', () => {
  let fixture: ComponentFixture<App>;

  beforeEach(async () => {
    localStorage.clear();
    await TestBed.configureTestingModule({
      imports: [App],
      providers: [
        provideRouter(routes),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(App);
    fixture.detectChanges();
    // Drive navigation explicitly: the redirect is async and the status
    // page only fires its API calls once activated.
    await TestBed.inject(Router).navigateByUrl('/status');
    fixture.detectChanges();
    // The status page fires API calls on init; answer them.
    const http = TestBed.inject(HttpTestingController);
    for (const req of http.match(() => true)) {
      if (req.request.url === '/healthz') {
        req.flush({ status: 'ok', version: 'test' });
      } else if (req.request.url === '/api/v1/doctor') {
        req.flush({ overall: 'ok', checks: [] });
      } else {
        req.flush({ state: 'UNCLAIMED', version: 'test' });
      }
    }
    fixture.detectChanges();
    http.verify();
  });

  it('renders the brand and primary nav', () => {
    const text = fixture.nativeElement.textContent as string;
    expect(text).toContain('Homestead');
    const link = fixture.nativeElement.querySelector('nav a[href="/status"]');
    expect(link?.textContent).toContain('Status');
  });

  it('offers login when logged out', () => {
    const link = fixture.nativeElement.querySelector('nav a[href="/login"]');
    expect(link?.textContent).toContain('Log in');
  });

  it('renders routed content in the outlet', () => {
    const text = fixture.nativeElement.textContent as string;
    expect(text).toContain('UNCLAIMED');
  });
});

describe('App logged in', () => {
  beforeEach(async () => {
    localStorage.clear();
    localStorage.setItem(
      SESSION_STORAGE_KEY,
      JSON.stringify({ token: 'token', username: 'owner', expiresAt: '' }),
    );
    await TestBed.configureTestingModule({
      imports: [App],
      providers: [
        provideRouter(routes),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
  });

  afterEach(() => {
    localStorage.clear();
  });

  it('shows the username with logout instead of login', () => {
    const fixture = TestBed.createComponent(App);
    fixture.detectChanges();
    const text = fixture.nativeElement.textContent as string;
    expect(text).toContain('owner');
    expect(
      fixture.nativeElement.querySelector('nav a[href="/login"]'),
    ).toBeNull();
    expect(
      fixture.nativeElement.querySelector('nav button')?.textContent,
    ).toContain('Log out');
  });
});
