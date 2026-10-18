import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { PlatformUpdatesPanel } from './platform-updates';

const VIEW = {
  running: { 'homestead-server': '0.0.0-s002', homesteadctl: 'unrecorded' },
  previous: {
    'homestead-server': {
      component: 'homestead-server',
      version: '0.0.0-s001',
      counter: 2,
      sha256: 'abc',
      path: '/usr/local/bin/homestead-server.previous',
    },
  },
  plan: {
    Components: [
      {
        Component: 'homestead-server',
        Current: '0.0.0-s002',
        Target: '1.0.0',
        Verification: 'verified',
      },
    ],
    Backup: true,
    BackupWhy: 'state',
    Warnings: [],
  },
  releases: [
    {
      component: 'homestead-server',
      version: '1.0.0',
      counter: 4,
      state: 'verified',
      refusal: '',
      detail: '',
      key_id: 'ABCD',
      sha256: '',
      override: false,
    },
    {
      component: 'homesteadctl',
      version: '',
      counter: 0,
      state: 'failed',
      refusal: 'authenticity',
      detail: 'release: signature key is not trusted',
      key_id: '',
      sha256: '',
      override: false,
      plan_error: 'refused: failed: release: signature key is not trusted',
    },
  ],
  refused: [
    {
      component: 'homesteadctl',
      version: '',
      counter: 0,
      state: 'failed',
      plan_error: 'refused: failed: release: signature key is not trusted',
    },
  ],
};

const KEYS = [
  { key_id: 'ABCD', public_key: 'x', label: 'HAI', source: 'embedded', placeholder: true },
];

describe('PlatformUpdatesPanel', () => {
  let fixture: ComponentFixture<PlatformUpdatesPanel>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [PlatformUpdatesPanel],
      providers: [provideHttpClient(), provideHttpClientTesting(), provideRouter([])],
    }).compileComponents();
    fixture = TestBed.createComponent(PlatformUpdatesPanel);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
    http.expectOne('/api/v1/platform/updates').flush(VIEW);
    http.expectOne('/api/v1/platform/trust-keys').flush(KEYS);
    fixture.detectChanges();
  });

  afterEach(() => http.verify());

  function text(): string {
    return fixture.nativeElement.textContent as string;
  }

  function reload(): void {
    http.expectOne('/api/v1/platform/updates').flush(VIEW);
    http.expectOne('/api/v1/platform/trust-keys').flush(KEYS);
  }

  it('shows verification state per release and trusted keys', () => {
    expect(text()).toContain('verified');
    expect(text()).toContain('failed');
    expect(text()).toContain('signature key is not trusted');
    expect(text()).toContain('ABCD (embedded)');
  });

  it('marks the placeholder key and lists refused releases separately', () => {
    expect(text()).toContain('Placeholder: verifies no real release');
    expect(text()).toContain('Refused (never planned)');
    expect(text()).toContain('homesteadctl -: refused: failed');
  });

  it('offers rollback override only after a rollback refusal', () => {
    expect(text()).not.toContain('I confirm installing an older release');
    const panel = fixture.componentInstance as any;
    panel.artifact = '/a';
    panel.checksums = '/b';
    panel.signature = '/c';
    panel.verify();
    const request = http.expectOne('/api/v1/platform/updates/verify');
    expect(request.request.body).toMatchObject({ allow_older: false });
    request.flush({
      component: 'homestead-server',
      version: '0.1.0',
      counter: 1,
      state: 'failed',
      refusal: 'rollback',
      detail: 'older',
    });
    reload();
    fixture.detectChanges();
    expect(text()).toContain('I confirm installing an older release');
  });

  it('adds and removes trusted keys through the API', () => {
    const panel = fixture.componentInstance as any;
    panel.newKey = 'RWabc';
    panel.addKey();
    const add = http.expectOne('/api/v1/platform/trust-keys');
    expect(add.request.method).toBe('POST');
    expect(add.request.body).toMatchObject({ public_key: 'RWabc' });
    add.flush({ key_id: 'EEFF', public_key: 'RWabc', label: '', source: 'owner' });
    reload();
    panel.removeKey('ABCD');
    const remove = http.expectOne('/api/v1/platform/trust-keys/ABCD');
    expect(remove.request.method).toBe('DELETE');
    remove.flush(null);
    reload();
  });

  it('applies a verified release only after confirmation, then links the operation', () => {
    const panel = fixture.componentInstance as any;
    panel.artifact = '/srv/rel/homestead-server';
    panel.checksums = '/srv/rel/SUMS';
    panel.signature = '/srv/rel/SUMS.minisig';
    panel.apply(VIEW.releases[0]);
    http.expectNone('/api/v1/platform/updates/apply');
    panel.applyConfirmed = true;
    panel.apply(VIEW.releases[0]);
    const request = http.expectOne('/api/v1/platform/updates/apply');
    expect(request.request.method).toBe('POST');
    expect(request.request.body).toEqual({
      component: 'homestead-server',
      artifact: '/srv/rel/homestead-server',
      checksums: '/srv/rel/SUMS',
      signature: '/srv/rel/SUMS.minisig',
      allow_older: false,
    });
    request.flush({ id: 21 });
    reload();
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/operations/21"]')).not.toBeNull();
  });

  it('asks for a separate older-release confirmation after an older refusal', () => {
    const panel = fixture.componentInstance as any;
    panel.artifact = '/srv/rel/homestead-server';
    panel.checksums = '/srv/rel/SUMS';
    panel.signature = '/srv/rel/SUMS.minisig';
    panel.applyConfirmed = true;
    panel.apply(VIEW.releases[0]);
    http
      .expectOne('/api/v1/platform/updates/apply')
      .flush('release is older than the installed release', {
        headers: { 'X-Homestead-Error': 'older-release-needs-override' },
        status: 422,
        statusText: 'Unprocessable Entity',
      });
    fixture.detectChanges();
    expect(text()).toContain('I confirm applying an older release');
    panel.applyOlderConfirmed = true;
    panel.applyConfirmed = true;
    panel.apply(VIEW.releases[0]);
    const retry = http.expectOne('/api/v1/platform/updates/apply');
    expect(retry.request.body).toMatchObject({ allow_older: true });
    retry.flush({ id: 22 });
    reload();
  });

  it('clears a stale apply marker through the API', () => {
    const panel = fixture.componentInstance as any;
    panel.clearApply();
    const request = http.expectOne('/api/v1/platform/updates/clear-apply');
    expect(request.request.method).toBe('POST');
    request.flush({
      op_id: 21,
      component: 'homestead-server',
      from_version: '0.1.0',
      to_version: '0.2.0',
    });
    reload();
    fixture.detectChanges();
    expect(text()).toContain('Cleared the pending apply of homestead-server');
  });

  it('shows each component\'s own running version', () => {
    expect(text()).toContain('homestead-server: 0.0.0-s002');
    expect(text()).toContain('homesteadctl: unrecorded');
  });

  it('rolls back only after confirmation, asking separately for an older release', () => {
    const panel = fixture.componentInstance as any;
    panel.rollback('homestead-server');
    http.expectNone('/api/v1/platform/updates/rollback');
    panel.rollbackConfirmed = true;
    panel.rollback('homestead-server');
    http
      .expectOne('/api/v1/platform/updates/rollback')
      .flush('platformapply: 0.0.0-s001 is older than the installed release', {
        headers: { 'X-Homestead-Error': 'older-release-needs-override' },
        status: 422,
        statusText: 'Unprocessable Entity',
      });
    fixture.detectChanges();
    expect(text()).toContain('I confirm rolling back to an older release');
    panel.rollbackOlderConfirmed = true;
    panel.rollbackConfirmed = true;
    panel.rollback('homestead-server');
    const retry = http.expectOne('/api/v1/platform/updates/rollback');
    expect(retry.request.body).toEqual({ component: 'homestead-server', allow_older: true });
    retry.flush({ id: 30 });
    reload();
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/operations/30"]')).not.toBeNull();
  });
});
