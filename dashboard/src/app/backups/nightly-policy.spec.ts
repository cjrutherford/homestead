import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { provideRouter } from '@angular/router';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting, HttpTestingController } from '@angular/common/http/testing';
import { BackupPolicyStatus } from '../core/api.service';
import { NightlyPolicy } from './nightly-policy';

function policyStatus(overrides: Partial<BackupPolicyStatus> = {}): BackupPolicyStatus {
  return {
    proposed: { time_of_day: '02:00', retain: 7, enabled: true },
    covered: ['jellyfin'],
    skipped: [{ package: 'wordcraft', reason: 'Keeps no data on the appliance.' }],
    time_zone: 'UTC',
    backup_storage: true,
    ...overrides,
  };
}

const PLAN = {
  policy: { time_of_day: '03:30', retain: 10, enabled: true },
  covered: ['jellyfin'],
  skipped: [],
  time_zone: 'UTC',
  changes: ['Back up every app below each night at 03:30, keeping the newest 10 runs of each.'],
  digest: 'd1',
};

describe('NightlyPolicy (S297)', () => {
  let fixture: ComponentFixture<NightlyPolicy>;
  let http: HttpTestingController;
  let approved: BackupPolicyStatus[];

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [NightlyPolicy],
      providers: [provideRouter([]), provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(NightlyPolicy);
    http = TestBed.inject(HttpTestingController);
    approved = [];
    fixture.componentInstance.approved.subscribe((s) => approved.push(s));
  });

  afterEach(() => http.verify());

  const el = () => fixture.nativeElement as HTMLElement;
  const text = () => (el().textContent ?? '').replace(/\s+/g, ' ');
  const button = (label: string) =>
    Array.from(el().querySelectorAll('button')).find((b) => b.textContent?.trim() === label);
  const input = (name: string) => el().querySelector(`input[name="${name}"]`) as HTMLInputElement;

  function load(status: BackupPolicyStatus) {
    fixture.detectChanges();
    http.expectOne('/api/v1/backups/policy').flush(status);
    fixture.detectChanges();
  }

  function review() {
    el().querySelector('form')!.dispatchEvent(new Event('submit'));
    fixture.detectChanges();
  }

  it('shows the proposal, what it covers and what it leaves out', () => {
    load(policyStatus());
    expect(text()).toContain('Not approved yet. Proposed: every night at 02:00 (UTC)');
    expect(text()).toContain('Covers: jellyfin.');
    expect(text()).toContain('wordcraft: Keeps no data on the appliance.');
    expect(input('policy-time').value).toBe('02:00');
    expect(input('policy-retain').value).toBe('7');
    expect(text()).not.toContain('need a disk');
  });

  it('explains and links to Storage without backup storage', () => {
    load(policyStatus({ backup_storage: false }));
    expect(text()).toContain('Nightly backups need a disk for the backup storage class.');
    expect(el().querySelector('a[href="/storage"]')).not.toBeNull();
  });

  it('plans, then approves exactly the reviewed request with its digest', () => {
    load(policyStatus());
    input('policy-time').value = '03:30';
    input('policy-time').dispatchEvent(new Event('input'));
    input('policy-retain').value = '10';
    input('policy-retain').dispatchEvent(new Event('input'));
    review();
    const plan = http.expectOne('/api/v1/backups/policy/plan');
    expect(plan.request.body).toEqual({ time_of_day: '03:30', retain: 10, enabled: true });
    plan.flush(PLAN);
    fixture.detectChanges();
    expect(text()).toContain('keeping the newest 10 runs');
    // "Every app below" names the plan's own coverage (S305).
    const el = fixture.nativeElement as HTMLElement;
    expect(el.querySelector('ul[aria-label="Apps the plan backs up"]')?.textContent).toContain('jellyfin');
    button('Approve the policy')!.click();
    const apply = http.expectOne('/api/v1/backups/policy/apply');
    expect(apply.request.body).toEqual({ time_of_day: '03:30', retain: 10, enabled: true, digest: 'd1' });
    apply.flush(policyStatus({ policy: { ...PLAN.policy, actor: 'owner' } }));
    fixture.detectChanges();
    expect(text()).toContain('Nightly backups are on.');
    expect(text()).toContain('Approved policy: on, every night at 03:30 (UTC)');
    expect(approved.length).toBe(1);
  });

  it('drops the plan when the input changes and on a stale digest', () => {
    load(policyStatus());
    review();
    http.expectOne('/api/v1/backups/policy/plan').flush(PLAN);
    fixture.detectChanges();
    input('policy-enabled').click();
    fixture.detectChanges();
    expect(button('Approve the policy')).toBeUndefined();
    review();
    const off = http.expectOne('/api/v1/backups/policy/plan');
    expect(off.request.body.enabled).toBe(false);
    off.flush({ ...PLAN, policy: { ...PLAN.policy, enabled: false }, digest: 'd2' });
    fixture.detectChanges();
    button('Approve the policy')!.click();
    http
      .expectOne('/api/v1/backups/policy/apply')
      .flush('the plan changed since it was reviewed; review it again', { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
    expect(button('Approve the policy')).toBeUndefined();
    expect(approved.length).toBe(0);
  });

  it('shows a refusal from the plan', () => {
    load(policyStatus({ backup_storage: false }));
    review();
    http
      .expectOne('/api/v1/backups/policy/plan')
      .flush('no disk holds the backup storage class', { status: 422, statusText: 'Unprocessable Entity' });
    fixture.detectChanges();
    expect(text()).toContain('no disk holds the backup storage class');
    expect(button('Approve the policy')).toBeUndefined();
  });
});
