import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { ConfigTransfer } from './config-transfer';

const DOC = {
  version: 1,
  exported_at: '2026-11-08T00:00:00Z',
  homestead: '0.0.1',
  store_schema: 1,
  desired_version: 1,
  settings: { hostname: 'other' },
  secrets: ['dns_api_token'],
};

describe('ConfigTransfer', () => {
  let fixture: ComponentFixture<ConfigTransfer>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ConfigTransfer],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(ConfigTransfer);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
  });

  afterEach(() => http.verify());

  const el = (): HTMLElement => fixture.nativeElement as HTMLElement;

  async function upload(text: string): Promise<void> {
    const input = el().querySelector('input[type=file]') as HTMLInputElement;
    const file = new File([text], 'c.yaml');
    Object.defineProperty(input, 'files', { value: [file], configurable: true });
    input.dispatchEvent(new Event('change'));
    await new Promise((r) => setTimeout(r, 0));
  }

  it('exports the YAML without asking for a password', () => {
    const button = [...el().querySelectorAll('button')].find((b) =>
      b.textContent?.includes('Export'),
    ) as HTMLButtonElement;
    button.click();
    const req = http.expectOne((r) => r.url === '/api/v1/config/export');
    expect(req.request.params.get('format')).toBe('yaml');
    expect(req.request.headers.has('X-Homestead-Reauth')).toBe(false);
    req.flush('version: 1\n');
    fixture.detectChanges();
    expect(el().textContent).toContain('Exported');
  });

  it('previews first, then imports with secrets only after confirm', async () => {
    await upload('version: 1');
    http.expectOne('/api/v1/config/import/preview').flush({
      valid: true,
      changes: [{ key: 'hostname', before: 'homestead', after: 'other' }],
      secrets: ['dns_api_token'],
      document: DOC,
    });
    fixture.detectChanges();
    expect(el().textContent).toContain('hostname');
    http.expectNone('/api/v1/config/import');
    const field = el().querySelector('input[type=password]') as HTMLInputElement;
    expect(field.autocomplete).toBe('off');
    field.value = 'typed-value';
    field.dispatchEvent(new Event('input'));
    (
      [...el().querySelectorAll('button')].find((b) =>
        b.textContent?.includes('Confirm import'),
      ) as HTMLButtonElement
    ).click();
    const req = http.expectOne('/api/v1/config/import');
    expect(req.request.body).toEqual({
      document: DOC,
      secrets: { dns_api_token: 'typed-value' },
    });
    req.flush({ settings: 1, secrets: 1 });
    fixture.detectChanges();
    expect(el().textContent).toContain('Imported 1 settings');
    expect(el().querySelector('input[type=password]')).toBeNull();
    expect(JSON.stringify(localStorage)).not.toContain('typed-value');
    expect(JSON.stringify(sessionStorage)).not.toContain('typed-value');
  });

  it('shows the daemon message for a file it cannot import and offers no confirm', async () => {
    await upload('junk');
    http
      .expectOne('/api/v1/config/import/preview')
      .flush({ valid: false, error: 'export version 99 newer', changes: [], secrets: [] });
    fixture.detectChanges();
    expect(el().textContent).toContain('export version 99 newer');
    expect(el().textContent).not.toContain('Confirm import');
  });

  it('cancel clears the preview', async () => {
    await upload('version: 1');
    http
      .expectOne('/api/v1/config/import/preview')
      .flush({ valid: true, changes: [], secrets: [], document: DOC });
    fixture.detectChanges();
    expect(el().textContent).toContain('No settings would change');
    (
      [...el().querySelectorAll('button')].find((b) =>
        b.textContent?.includes('Cancel'),
      ) as HTMLButtonElement
    ).click();
    fixture.detectChanges();
    expect(el().textContent).not.toContain('Review import');
  });
});
