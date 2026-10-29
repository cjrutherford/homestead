import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { CraftImagesPanel } from './craft-images';

const STATUS = {
  images: [
    {
      app: 'wordcraft',
      version: '1.0.0',
      tag: 'homestead/craft-wordcraft:1.0.0',
      image_id: 'sha256:aaa',
      counter: 3,
      key_id: 'K',
      artifact_sha256: 'x',
      context_digest: 'sha256:y',
      built_at: '',
      present: true,
      live_id: 'sha256:bbb',
      match: false,
    },
  ],
  verdicts: [
    {
      app: 'wordcraft',
      version: '1.0.0',
      counter: 3,
      state: 'failed',
      refusal: 'artifact-digest',
      detail: 'artifact SHA-256 does not match',
      key_id: 'K',
      override: false,
    },
  ],
  baselines: {},
  manifest_path: '/usr/local/share/homestead/catalog/craft/craft-manifest.yaml',
  signature_path: '',
  context_dir: '',
};

const KEYS = [
  { key_id: 'CCCC', public_key: 'x', label: 'HAI craft', source: 'embedded', placeholder: true },
];

describe('CraftImagesPanel', () => {
  let fixture: ComponentFixture<CraftImagesPanel>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [CraftImagesPanel],
      providers: [provideHttpClient(), provideHttpClientTesting(), provideRouter([])],
    }).compileComponents();
    fixture = TestBed.createComponent(CraftImagesPanel);
    http = TestBed.inject(HttpTestingController);
    fixture.detectChanges();
    http.expectOne('/api/v1/craft').flush(STATUS);
    http.expectOne('/api/v1/craft/trust-keys').flush(KEYS);
    fixture.detectChanges();
  });

  afterEach(() => http.verify());

  const text = () => fixture.nativeElement.textContent as string;

  function reload(): void {
    http.expectOne('/api/v1/craft').flush(STATUS);
    http.expectOne('/api/v1/craft/trust-keys').flush(KEYS);
  }

  it('shows recorded images against their live digest, the failing check, and keys', () => {
    expect(text()).toContain('homestead/craft-wordcraft:1.0.0');
    expect(text()).toContain('differs from the recorded digest (sha256:bbb)');
    expect(text()).toContain('(artifact-digest check)');
    expect(text()).toContain('CCCC (embedded)');
    expect(text()).toContain('Placeholder: verifies no real craft app list.');
  });

  it('plans a manifest and offers the override only after a counter refusal', () => {
    const panel = fixture.componentInstance as any;
    expect(text()).not.toContain('I confirm building from an older craft app list');
    panel.app = 'wordcraft';
    panel.plan();
    const request = http.expectOne('/api/v1/craft/plan');
    expect(request.request.body).toMatchObject({ app: 'wordcraft', allow_older: false });
    request.flush({
      plan: {},
      verdict: { app: 'wordcraft', state: 'failed', refusal: 'counter', detail: 'older' },
    });
    reload();
    fixture.detectChanges();
    expect(text()).toContain('I confirm building from an older craft app list');
  });

  it('offers no override for an artifact digest refusal', () => {
    const panel = fixture.componentInstance as any;
    panel.app = 'wordcraft';
    panel.plan();
    http.expectOne('/api/v1/craft/plan').flush({
      plan: {},
      verdict: { app: 'wordcraft', state: 'failed', refusal: 'artifact-digest', detail: 'bad' },
    });
    reload();
    fixture.detectChanges();
    expect(text()).not.toContain('I confirm building from an older craft app list');
    expect(text()).toContain('Refused (artifact-digest check): bad');
  });

  it('starts a build and links the operation', () => {
    const panel = fixture.componentInstance as any;
    panel.app = 'wordcraft';
    panel.artifact = '/srv/wordcraft.tar.gz';
    panel.build();
    const request = http.expectOne('/api/v1/craft/build');
    expect(request.request.body).toMatchObject({
      app: 'wordcraft',
      artifact: '/srv/wordcraft.tar.gz',
    });
    request.flush({ id: 9 });
    reload();
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('app-operation-progress a[href="/admin/operations/9"]')).not.toBeNull();
  });

  it('adds and removes craft keys through the craft routes', () => {
    const panel = fixture.componentInstance as any;
    panel.newKey = 'RWabc';
    panel.addKey();
    const add = http.expectOne('/api/v1/craft/trust-keys');
    expect(add.request.method).toBe('POST');
    add.flush({ key_id: 'EEFF', public_key: 'RWabc', label: '', source: 'owner' });
    reload();
    panel.removeKey('CCCC');
    const remove = http.expectOne('/api/v1/craft/trust-keys/CCCC');
    expect(remove.request.method).toBe('DELETE');
    remove.flush(null);
    reload();
  });
});
