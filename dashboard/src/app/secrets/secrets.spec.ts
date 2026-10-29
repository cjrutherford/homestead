import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Secrets } from './secrets';

describe('Secrets', () => {
  let fixture: ComponentFixture<Secrets>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Secrets],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Secrets);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
    http
      .expectOne('/api/v1/secrets/key')
      .flush({ version: 2, in_progress: false });
    fixture.detectChanges();
  });

  afterEach(() => http.verify());

  function buttons(): HTMLButtonElement[] {
    return Array.from(fixture.nativeElement.querySelectorAll('button'));
  }

  it('shows the key version and sends nothing until confirmed', () => {
    expect(fixture.nativeElement.textContent).toContain('Current key version: 2');
    buttons().find((b) => b.textContent?.includes('Re-key secrets'))!.click();
    fixture.detectChanges();
    expect(fixture.nativeElement.textContent).toContain('recovery');
    http.expectNone('/api/v1/secrets/rekey');
  });

  it('cancel withdraws the confirmation', () => {
    buttons().find((b) => b.textContent?.includes('Re-key secrets'))!.click();
    fixture.detectChanges();
    buttons().find((b) => b.textContent?.includes('Cancel'))!.click();
    fixture.detectChanges();
    expect(fixture.nativeElement.textContent).not.toContain('Confirm re-key');
  });

  it('confirmed re-key posts confirm and links the operation', () => {
    buttons().find((b) => b.textContent?.includes('Re-key secrets'))!.click();
    fixture.detectChanges();
    buttons().find((b) => b.textContent?.includes('Confirm re-key'))!.click();
    const req = http.expectOne('/api/v1/secrets/rekey');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ confirm: true });
    req.flush({ id: 9 });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/admin/operations/9"]')).not.toBeNull();
  });

  it('surfaces a failed re-key through the state message', () => {
    buttons().find((b) => b.textContent?.includes('Re-key secrets'))!.click();
    fixture.detectChanges();
    buttons().find((b) => b.textContent?.includes('Confirm re-key'))!.click();
    http
      .expectOne('/api/v1/secrets/rekey')
      .flush('re-key already in progress', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[role="alert"]')).toBeTruthy();
  });
});
