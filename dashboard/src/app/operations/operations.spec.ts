import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Operations } from './operations';

describe('Operations', () => {
  let fixture: ComponentFixture<Operations>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Operations],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Operations);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => {
    http.verify();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('lists operations newest first', () => {
    const req = http.expectOne('/api/v1/operations');
    expect(req.request.method).toBe('GET');
    req.flush([
      { id: 2, type: 'b.two', status: 'running', steps: [] },
      { id: 1, type: 'a.one', status: 'succeeded', steps: [] },
    ]);
    fixture.detectChanges();
    expect(text()).toContain('B two');
    expect(text()).toContain('Running');
    const link = fixture.nativeElement.querySelector('a[href="/operations/2"]');
    expect(link).not.toBeNull();
  });

  it('reports an empty trail', () => {
    http.expectOne('/api/v1/operations').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('No operations yet.');
  });

  it('reports unreachability', () => {
    http.expectOne('/api/v1/operations').flush('down', {
      status: 503,
      statusText: 'Unavailable',
    });
    fixture.detectChanges();
    expect(text()).toContain("Can't reach the appliance");
  });
});
