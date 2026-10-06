import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Firewall } from './firewall';

const STATUS = {
  nft_available: true,
  ruleset_accessible: true,
  managed: false,
  rule_count: 0,
  daemon_port: 8080,
  local_subnets: ['192.168.1.0/24'],
};

const PLAN = {
  in_sync: false,
  changes: [{ action: 'create-table', rule: '', detail: 'create' }],
  script: 'table inet homestead',
};

describe('Firewall', () => {
  let fixture: ComponentFixture<Firewall>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Firewall],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Firewall);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  function answerBoot(sync: boolean): void {
    http.expectOne('/api/v1/firewall/status').flush(STATUS);
    const plan = http.expectOne('/api/v1/firewall/plan');
    plan.flush(sync ? { ...PLAN, in_sync: true, changes: [] } : PLAN);
    fixture.detectChanges();
    // Progress resume lookup fires unless already in sync.
    if (!sync) {
      http.expectOne('/api/v1/wizards/firewall').flush('none', {
        status: 404,
        statusText: 'Not Found',
      });
      fixture.detectChanges();
    }
  }

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('identifies LAN subnets first', () => {
    fixture.detectChanges();
    answerBoot(false);
    expect(text()).toContain('192.168.1.0/24');
  });

  it('shows recommended rules then reviews the diff', () => {
    fixture.detectChanges();
    answerBoot(false);
    fixture.componentInstance['next']();
    http.expectOne('/api/v1/wizards/firewall').flush({
      wizard: 'firewall',
      step: 'rules',
      payload: {},
      updated_at: '',
    });
    fixture.detectChanges();
    expect(text()).toContain('table inet homestead');
    fixture.componentInstance['next']();
    http.expectOne('/api/v1/wizards/firewall').flush({
      wizard: 'firewall',
      step: 'review',
      payload: {},
      updated_at: '',
    });
    fixture.detectChanges();
    expect(text()).toContain('Review changes');
    expect(text()).toContain('create-table');
  });

  it('applies on confirm and lands on done', () => {
    fixture.detectChanges();
    answerBoot(false);
    fixture.componentInstance['next']();
    http.expectOne('/api/v1/wizards/firewall').flush({
      wizard: 'firewall',
      step: 'rules',
      payload: {},
      updated_at: '',
    });
    fixture.detectChanges();
    fixture.componentInstance['next']();
    http.expectOne('/api/v1/wizards/firewall').flush({
      wizard: 'firewall',
      step: 'review',
      payload: {},
      updated_at: '',
    });
    fixture.detectChanges();
    fixture.componentInstance['confirm']();
    http.expectOne('/api/v1/firewall/apply').flush({ applied: true });
    fixture.detectChanges();
    http.expectOne('/api/v1/wizards/firewall').flush({
      wizard: 'firewall',
      step: 'done',
      payload: {},
      updated_at: '',
    });
    fixture.detectChanges();
    expect(text()).toContain('Firewall applied and verified');
  });

  it('reports guard refusals without leaving review', () => {
    fixture.detectChanges();
    answerBoot(false);
    fixture.componentInstance['next']();
    http.expectOne('/api/v1/wizards/firewall').flush({
      wizard: 'firewall',
      step: 'rules',
      payload: {},
      updated_at: '',
    });
    fixture.detectChanges();
    fixture.componentInstance['next']();
    http.expectOne('/api/v1/wizards/firewall').flush({
      wizard: 'firewall',
      step: 'review',
      payload: {},
      updated_at: '',
    });
    fixture.detectChanges();
    fixture.componentInstance['confirm']();
    http.expectOne('/api/v1/firewall/apply').flush('managed firewall disabled', {
      status: 409,
      statusText: 'Conflict',
    });
    fixture.detectChanges();
    expect(text()).toContain('managed firewall disabled');
    expect(text()).toContain('Review changes');
  });

  it('lands on done when already in sync', () => {
    fixture.detectChanges();
    answerBoot(true);
    expect(text()).toContain('already matches');
  });
});
