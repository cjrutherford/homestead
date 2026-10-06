import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Config } from './config';

const FIELDS = [
  { name: 'hostname', type: 'string', description: 'Name.', required: true },
  { name: 'web_port', type: 'integer', description: 'Port.', default: 8080 },
];

describe('Config', () => {
  let fixture: ComponentFixture<Config>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Config],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(Config);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    http.verify();
  });

  function answerLoad(settings: Record<string, unknown> = {}): void {
    http.expectOne('/api/v1/config/schema').flush({ fields: FIELDS });
    http.expectOne('/api/v1/config').flush({ version: 1, settings });
    fixture.detectChanges();
  }

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  function review(values: Record<string, unknown>): void {
    fixture.componentInstance['review'](values);
  }

  it('loads schema and current values', () => {
    answerLoad({ hostname: 'hs' });
    expect(text()).toContain('Configuration');
    expect(text()).toContain('Name.');
  });

  it('reviews valid submissions with a diff', () => {
    answerLoad({ hostname: 'hs', web_port: 8080 });
    review({ hostname: 'new', web_port: 8080 });
    http.expectOne('/api/v1/config/plan').flush({
      valid: true,
      effective: { hostname: 'new', web_port: 8080 },
    });
    fixture.detectChanges();
    expect(text()).toContain('Review changes');
    expect(text()).toContain('hostname');
    expect(text()).toContain('new');
  });

  it('reports no changes when identical', () => {
    answerLoad({ hostname: 'hs' });
    review({ hostname: 'hs' });
    http.expectOne('/api/v1/config/plan').flush({
      valid: true,
      effective: { hostname: 'hs' },
    });
    fixture.detectChanges();
    expect(text()).toContain('No changes.');
  });

  it('displays plan errors without leaving edit', () => {
    answerLoad({});
    review({ web_port: 'lots' });
    http.expectOne('/api/v1/config/plan').flush({
      valid: false,
      errors: [{ field: 'web_port', reason: 'want integer' }],
    });
    fixture.detectChanges();
    expect(text()).toContain('want integer');
    expect(text()).not.toContain('Review changes');
  });

  it('applies on confirm and reports saved', () => {
    answerLoad({ hostname: 'hs' });
    review({ hostname: 'new' });
    http.expectOne('/api/v1/config/plan').flush({
      valid: true,
      effective: { hostname: 'new' },
    });
    fixture.detectChanges();
    fixture.componentInstance['confirm']();
    const put = http.expectOne('/api/v1/config');
    expect(put.request.method).toBe('PUT');
    put.flush({ version: 1, settings: { hostname: 'new' } });
    fixture.detectChanges();
    expect(text()).toContain('Configuration saved.');
  });

  it('reverts to the saved configuration', () => {
    answerLoad({ hostname: 'hs' });
    fixture.componentInstance['revert']();
    http.expectOne('/api/v1/config').flush({ version: 1, settings: { hostname: 'srv' } });
    fixture.detectChanges();
    expect(text()).toContain('Reverted to saved configuration.');
  });
});
