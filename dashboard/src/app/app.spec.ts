import { describe, expect, it, beforeEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { App } from './app';
import { routes } from './app.routes';

describe('App', () => {
  let fixture: ComponentFixture<App>;

  beforeEach(async () => {
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
      req.flush(req.request.url === '/healthz'
        ? { status: 'ok', version: 'test' }
        : { state: 'UNCLAIMED', version: 'test' });
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

  it('renders routed content in the outlet', () => {
    const text = fixture.nativeElement.textContent as string;
    expect(text).toContain('UNCLAIMED');
  });
});
