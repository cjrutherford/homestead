import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Tunnel } from './tunnel';

const STATUS = {
  preferred: 'tunnel',
  gateway: '',
  interface: '',
  protocols: [],
  tunnels: [
    { id: 'tunnel-id', name: 'homestead', status: 'healthy', connections: [] },
  ],
  routes: [
    {
      Hostname: 'app.example.com',
      TunnelID: 'tunnel-id',
      Service: 'http://127.0.0.1:8080',
    },
  ],
  connector: { installed: true, active: true },
  accounts: 1,
};

describe('Tunnel', () => {
  let fixture: ComponentFixture<Tunnel>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Tunnel],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Tunnel);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('leads with the tunnel when the router is silent', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    expect(text()).toContain('Preferred path: tunnel');
    expect(text()).toContain('Nothing on the router needs changing');
    expect(text()).toContain('homestead: healthy');
    expect(text()).toContain('app.example.com');
    expect(text()).toContain('http://127.0.0.1:8080');
  });

  it('sets up through operations without echoing the token', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    fixture.componentInstance['setup'](
      'homestead',
      'app.example.com',
      'http://127.0.0.1:8080',
      'sealed-token',
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    );
    const setup = http.expectOne('/api/v1/tunnel/setup');
    expect(setup.request.body).toMatchObject({
      name: 'homestead',
      api_token: 'sealed-token',
    });
    setup.flush({ id: 23 });
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/operations/23"]')).not.toBeNull();
    expect(text()).not.toContain('sealed-token');
  });

  it('tears down with an explicit account', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    fixture.componentInstance['teardown'](
      'tunnel-id',
      'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    );
    const teardown = http.expectOne(
      '/api/v1/tunnel/tunnels/tunnel-id?account_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    );
    expect(teardown.request.method).toBe('DELETE');
    teardown.flush({ id: 24 });
    http.expectOne('/api/v1/tunnel/status').flush({ ...STATUS, tunnels: [] });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/operations/24"]')).not.toBeNull();
  });

  it('asks before tearing down and only then deletes', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    const input = el.querySelector('.risky input[name="account-id"]') as HTMLInputElement;
    input.value = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
    (el.querySelector('.risky form') as HTMLFormElement).dispatchEvent(new Event('submit'));
    fixture.detectChanges();
    http.expectNone((req) => req.method === 'DELETE');
    expect(el.querySelector('app-dialog')?.textContent).toContain('Tear down this tunnel?');
    const confirm = Array.from(el.querySelectorAll('app-dialog button')).find(
      (b) => b.textContent?.trim() === 'Confirm tear down',
    ) as HTMLButtonElement;
    confirm.click();
    http
      .expectOne('/api/v1/tunnel/tunnels/tunnel-id?account_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')
      .flush({ id: 5 });
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
  });

  it('shows missing setup fields next to the field', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/tunnel/status').flush(STATUS);
    fixture.detectChanges();
    const el = fixture.nativeElement as HTMLElement;
    const forms = el.querySelectorAll('form');
    forms[forms.length - 1].dispatchEvent(new Event('submit'));
    fixture.detectChanges();
    const name = el.querySelector('#tunnel-name') as HTMLInputElement;
    expect(name.getAttribute('aria-invalid')).toBe('true');
    expect(el.querySelector('#tunnel-name-error')?.textContent).toContain('Enter a name');
    expect(el.querySelectorAll('.field-error').length).toBe(5);
  });
});
