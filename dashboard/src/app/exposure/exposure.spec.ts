import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { Exposure } from './exposure';

const PLAN = {
  Host: 'app.example.com',
  From: 'local',
  To: 'public',
  Assessment: {
    Aligned: true,
    Recommended: 'public',
    Findings: [{ Severity: 'info', Message: 'Wide source needs CIDRs.' }],
  },
  DNS: [{ Name: 'app', Type: 'A', Value: '203.0.113.7' }],
  TLS: { Action: 'issue', Name: 'app.example.com', Detail: 'DNS-01' },
  Firewall: [
    {
      Service: { Name: 'web-https', Protocol: 'tcp', Port: 443 },
      Sources: [{ Zone: 'internet', CIDR: '' }],
    },
  ],
  Edge: { Host: 'app.example.com', Exposure: 'public' },
};

describe('Exposure', () => {
  let fixture: ComponentFixture<Exposure>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [Exposure],
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(Exposure);
    http = TestBed.inject(HttpTestingController);
  });

  afterEach(() => {
    http.verify();
  });

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  it('reviews the recomputed plan', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/exposure/review').flush(PLAN);
    http.expectOne('/api/v1/exposure/approvals').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('app.example.com: local → public');
    expect(text()).toContain('Wide source needs CIDRs.');
    expect(text()).toContain('A app → 203.0.113.7');
    expect(text()).toContain('tcp/443');
  });

  it('asks for a selection when nothing is saved', () => {
    fixture.detectChanges();
    http
      .expectOne('/api/v1/exposure/review')
      .flush('no saved selection', { status: 404, statusText: 'Not Found' });
    http.expectOne('/api/v1/exposure/approvals').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('No reviewed selection yet');
  });

  it('approves under a named actor and applies', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/exposure/review').flush(PLAN);
    http.expectOne('/api/v1/exposure/approvals').flush([]);
    fixture.detectChanges();
    fixture.componentInstance['approve']('owner', '203.0.113.0/24');
    const approve = http.expectOne('/api/v1/exposure/approvals');
    expect(approve.request.body).toMatchObject({
      actor: 'owner',
      networks: ['203.0.113.0/24'],
    });
    approve.flush({ host: 'app.example.com', actor: 'owner' });
    http.expectOne('/api/v1/exposure/review').flush(PLAN);
    http
      .expectOne('/api/v1/exposure/approvals')
      .flush([{ host: 'app.example.com', actor: 'owner' }]);
    fixture.detectChanges();
    expect(text()).toContain('Approved app.example.com by owner.');
    fixture.componentInstance['apply']('app.example.com');
    const apply = http.expectOne('/api/v1/exposure/apply');
    expect(apply.request.body).toMatchObject({ host: 'app.example.com' });
    apply.flush({ id: 17 });
    fixture.detectChanges();
    expect(text()).toContain('Follow operation 17');
  });
});
