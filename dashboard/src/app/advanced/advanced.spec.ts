import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Advanced } from './advanced';

const LINK = {
  host: 'dockge.lan',
  url: 'https://dockge.lan',
  exposure: 'local',
  tls: 'internal',
  aligned: true,
  findings: [],
  installed: true,
};

describe('Advanced', () => {
  let fixture: ComponentFixture<Advanced>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Advanced],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Advanced);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('links the interface with its access policy', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/dockge?domain=lan').flush(LINK);
    fixture.detectChanges();
    const anchor = fixture.nativeElement.querySelector(
      'a',
    ) as HTMLAnchorElement | null;
    expect(anchor?.getAttribute('href')).toBe('https://dockge.lan');
    expect(text()).toContain('local only');
    expect(text()).toContain('running');
  });

  it('reloads the link for another domain', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/dockge?domain=lan').flush(LINK);
    fixture.detectChanges();
    fixture.componentInstance['setDomain']('home.arpa');
    fixture.componentInstance['load']();
    http
      .expectOne('/api/v1/dockge?domain=home.arpa')
      .flush({ ...LINK, host: 'dockge.home.arpa', installed: false });
    fixture.detectChanges();
    expect(text()).toContain('not running');
  });

  it('reports failures without a stale link', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/dockge?domain=lan').flush(LINK);
    fixture.detectChanges();
    fixture.componentInstance['load']();
    http
      .expectOne('/api/v1/dockge?domain=lan')
      .flush('no such domain', { status: 400, statusText: 'Bad Request' });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('a')).toBeNull();
  });
});
