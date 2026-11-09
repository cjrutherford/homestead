import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { Names } from './names';
import { NamesStatus, ResolverPlan } from '../core/api.service';

const STATUS: NamesStatus = {
  resolver: {
    enabled: false,
    running: false,
    listening: [],
    names: ['homestead.lan', 'immich.lan'],
    router_hint: 'On your router, add a conditional forward for the domain "lan" to this appliance.',
  },
  apps: [
    { host: 'homestead.lan', names: ['homestead.lan', 'homestead.local'] },
    { host: 'immich.lan', names: ['immich.lan', 'immich.local'] },
  ],
};

const PLAN: ResolverPlan = {
  enabled: true,
  changes: ['Answer DNS for this appliance\'s .lan names on port 53.'],
  listen: ['192.168.1.10:53'],
  names: ['homestead.lan', 'immich.lan'],
  firewall: [
    { name: 'local-dns-udp-local', detail: 'accept udp/53 from 192.168.1.0/24' },
    { name: 'local-dns-udp-public-drop', detail: 'drop udp/53 from everywhere else' },
  ],
  warnings: [],
  router_hint: STATUS.resolver.router_hint,
  digest: 'd1g',
};

describe('Names', () => {
  let fixture: ComponentFixture<Names>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Names],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(Names);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => http.verify());

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  function load(status: NamesStatus = STATUS): void {
    fixture.detectChanges();
    http.expectOne('/api/v1/names').flush(status);
    fixture.detectChanges();
  }

  it('lists each app\'s .local name and says how to forward .lan', () => {
    load();
    const table = (fixture.nativeElement as HTMLElement).querySelector('table[aria-label="App names"]')?.textContent ?? '';
    expect(table).toContain('immich.lan');
    expect(table).toContain('immich.local');
    expect(text()).toContain('off');
    expect(text()).toContain('conditional forward');
  });

  it('shows the plan and changes nothing until approved', () => {
    load();
    fixture.componentInstance.review(true);
    const plan = http.expectOne('/api/v1/names/resolver/plan');
    expect(plan.request.body).toEqual({ enabled: true });
    plan.flush(PLAN);
    fixture.detectChanges();
    http.expectNone('/api/v1/names/resolver/apply');
    const rules = (fixture.nativeElement as HTMLElement).querySelector('table[aria-label="Firewall rules"]')?.textContent ?? '';
    expect(rules).toContain('local-dns-udp-public-drop');
    expect(text()).toContain('192.168.1.10:53');
  });

  it('approves the reviewed plan by its digest', () => {
    load();
    fixture.componentInstance.review(true);
    http.expectOne('/api/v1/names/resolver/plan').flush(PLAN);
    fixture.detectChanges();
    const approve = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === 'Approve and turn on',
    ) as HTMLButtonElement;
    approve.click();
    const apply = http.expectOne('/api/v1/names/resolver/apply');
    expect(apply.request.body).toEqual({ enabled: true, digest: 'd1g' });
    apply.flush({ ...STATUS, resolver: { ...STATUS.resolver, enabled: true, running: true, listening: ['192.168.1.10:53'] } });
    fixture.detectChanges();
    expect(text()).toContain('answering on 192.168.1.10:53');
    expect(text()).toContain('Apply the firewall plan');
  });

  it('asks for a fresh review when the plan changed', () => {
    load();
    fixture.componentInstance.review(true);
    http.expectOne('/api/v1/names/resolver/plan').flush(PLAN);
    fixture.componentInstance.approve();
    http.expectOne('/api/v1/names/resolver/apply').flush('changed', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(text()).toContain('Review it again');
    expect((fixture.nativeElement as HTMLElement).querySelector('table[aria-label="Firewall rules"]')).toBeNull();
  });

  it('warns when the resolver is on but not answering', () => {
    load({ ...STATUS, resolver: { ...STATUS.resolver, enabled: true, error: 'listen udp 192.168.1.10:53: address already in use' } });
    expect(text()).toContain('on, not answering');
    expect(text()).toContain('address already in use');
    expect(text()).toContain('Review turning it off');
  });
});
