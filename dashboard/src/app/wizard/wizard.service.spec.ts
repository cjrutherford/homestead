import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { WizardService } from './wizard.service';

describe('WizardService', () => {
  let service: WizardService;
  let http: HttpTestingController;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    service = TestBed.inject(WizardService);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  it('loads saved progress', () => {
    service.load('claim').subscribe((state) => {
      expect(state.step).toBe('admin');
    });
    const req = http.expectOne('/api/v1/wizards/claim');
    expect(req.request.method).toBe('GET');
    req.flush({ wizard: 'claim', step: 'admin', payload: {}, updated_at: '' });
  });

  it('saves progress', () => {
    service.save('claim', 'done', {}).subscribe((state) => {
      expect(state.step).toBe('done');
    });
    const req = http.expectOne('/api/v1/wizards/claim');
    expect(req.request.method).toBe('PUT');
    expect(req.request.body).toEqual({ step: 'done', payload: {} });
    req.flush({ wizard: 'claim', step: 'done', payload: {}, updated_at: '' });
  });

  it('clears progress', () => {
    service.clear('claim').subscribe();
    const req = http.expectOne('/api/v1/wizards/claim');
    expect(req.request.method).toBe('DELETE');
    req.flush(null);
  });
});
