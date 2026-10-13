import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { StorageSetup } from './storage';

const MOUNT = {
  ID: 1,
  Device: '/dev/sdb1',
  Mountpoint: '/mnt/data',
  FSType: 'ext4',
  Options: null,
  ReadOnly: false,
  UUID: 'u1',
  Label: 'disk',
  Accessible: true,
  TotalBytes: 2 * 1024 ** 4,
  FreeBytes: 1024 ** 4,
  AvailBytes: 1024 ** 4,
};
const RO = { ...MOUNT, ID: 2, Mountpoint: '/mnt/ro', ReadOnly: true };
const DEAD = { ...MOUNT, ID: 3, Mountpoint: '/mnt/dead', Accessible: false };
const REG = {
  Mountpoint: '/mnt/data',
  Label: 'main',
  Actor: 'chris',
  AddedAt: '2026-01-01T00:00:00Z',
};
const ASSIGN = {
  Class: 'bulk',
  Mountpoint: '/mnt/data',
  Actor: 'chris',
  CreatedAt: '2026-01-01T00:00:00Z',
};

describe('StorageSetup', () => {
  let fixture: ComponentFixture<StorageSetup>;
  let http: HttpTestingController;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [StorageSetup],
      providers: [provideHttpClient(), provideHttpClientTesting()],
    }).compileComponents();
    fixture = TestBed.createComponent(StorageSetup);
    http = TestBed.inject(HttpTestingController);
  });
  afterEach(() => http.verify());

  const text = () => fixture.nativeElement.textContent as string;
  const click = (label: string) => {
    const button = Array.from(
      fixture.nativeElement.querySelectorAll('button') as NodeListOf<HTMLButtonElement>,
    ).find((b) => b.textContent?.trim() === label);
    expect(button, label).toBeTruthy();
    button!.click();
    fixture.detectChanges();
  };

  function load(mounts: unknown[], regs: unknown[], assigns: unknown[]) {
    fixture.detectChanges();
    http.expectOne('/api/v1/storage/mounts').flush(mounts);
    http.expectOne('/api/v1/storage/registrations').flush(regs);
    http.expectOne('/api/v1/storage/assignments').flush(assigns);
    fixture.detectChanges();
  }

  it('shows loading before data arrives', () => {
    fixture.detectChanges();
    expect(text()).toContain('Discovering mounts');
    expect(text()).toContain('Loading registrations');
    expect(text()).toContain('Loading assignments');
    http.match(() => true).forEach((r) => r.flush([]));
  });

  it('shows empty states', () => {
    load([], [], []);
    expect(text()).toContain('No mounts discovered.');
    expect(text()).toContain('No mounts registered yet.');
    expect(text()).toContain('No classes assigned yet.');
  });

  it('lists mounts with capacity, access, read-only and registration', () => {
    load([MOUNT, RO, DEAD], [REG], [ASSIGN]);
    expect(text()).toContain('/mnt/data');
    expect(text()).toContain('1.0 TiB free of 2.0 TiB');
    expect(text()).toContain('registered as main');
    expect(text()).toContain('holds bulk');
    expect(text()).toContain('Cannot be registered: read-only');
    expect(text()).toContain('Cannot be registered: serves no usage data');
    expect(text()).toContain('capacity unavailable');
  });

  it('shows per-section errors', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/storage/mounts').flush('boom', { status: 500, statusText: 'x' });
    http.expectOne('/api/v1/storage/registrations').flush('nope', { status: 500, statusText: 'x' });
    http.expectOne('/api/v1/storage/assignments').flush('bad', { status: 500, statusText: 'x' });
    fixture.detectChanges();
    expect(text()).toContain('boom');
    expect(text()).toContain('nope');
    expect(text()).toContain('bad');
  });

  it('prefills the registration form from a discovered mount', () => {
    load([MOUNT], [], []);
    click('Use for registration');
    const input = fixture.nativeElement.querySelector('input[name=mountpoint]') as HTMLInputElement;
    expect(input.value).toBe('/mnt/data');
  });

  it('validates registration input without calling the API', () => {
    load([MOUNT], [], []);
    const c = fixture.componentInstance;
    c.register('', 'main', 'chris');
    fixture.detectChanges();
    expect(text()).toContain('needs a mountpoint');
    c.register('mnt/data', 'main', 'chris');
    fixture.detectChanges();
    expect(text()).toContain('absolute path');
    c.register('/mnt/data', 'Main_1', 'chris');
    fixture.detectChanges();
    expect(text()).toContain('Label must start');
    c.register('/mnt/data', 'main', ' ');
    fixture.detectChanges();
    expect(text()).toContain('named actor');
  });

  it('registers a mount and reloads', () => {
    load([MOUNT], [], []);
    fixture.componentInstance.register(' /mnt/data ', ' main ', 'chris');
    const req = http.expectOne('/api/v1/storage/registrations');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ mountpoint: '/mnt/data', label: 'main', actor: 'chris' });
    req.flush(REG);
    http.expectOne('/api/v1/storage/mounts').flush([MOUNT]);
    http.expectOne('/api/v1/storage/registrations').flush([REG]);
    http.expectOne('/api/v1/storage/assignments').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('Registered /mnt/data as main.');
  });

  it('surfaces the daemon refusal for registration', () => {
    load([MOUNT], [], []);
    fixture.componentInstance.register('/mnt/data', 'main', 'chris');
    http
      .expectOne('/api/v1/storage/registrations')
      .flush('storage: "/mnt/data" is read-only', { status: 422, statusText: 'x' });
    fixture.detectChanges();
    expect(text()).toContain('is read-only');
  });

  it('confirms before renaming an already registered mount', () => {
    load([MOUNT], [REG], []);
    fixture.componentInstance.register('/mnt/data', 'primary', 'chris');
    fixture.detectChanges();
    http.expectNone('/api/v1/storage/registrations');
    expect(text()).toContain('is already registered. This changes');
    click('Cancel');
    expect(text()).not.toContain('is already registered. This changes');
    fixture.componentInstance.register('/mnt/data', 'primary', 'chris');
    fixture.detectChanges();
    click('Confirm rename');
    http.expectOne('/api/v1/storage/registrations').flush({ ...REG, Label: 'primary' });
    http.match(() => true).forEach((r) => r.flush([]));
  });

  it('validates assignment input', () => {
    load([MOUNT], [REG], []);
    const c = fixture.componentInstance;
    c.assign('floppy', '/mnt/data', 'chris');
    fixture.detectChanges();
    expect(text()).toContain('Choose a storage class');
    c.assign('bulk', '', 'chris');
    fixture.detectChanges();
    expect(text()).toContain('registered mount and a named actor');
  });

  it('assigns a class to a new mount without confirmation', () => {
    load([MOUNT], [REG], []);
    fixture.componentInstance.assign('bulk', '/mnt/data', 'chris');
    const req = http.expectOne('/api/v1/storage/assignments');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ class: 'bulk', mountpoint: '/mnt/data', actor: 'chris' });
    req.flush(ASSIGN);
    http.expectOne('/api/v1/storage/mounts').flush([MOUNT]);
    http.expectOne('/api/v1/storage/registrations').flush([REG]);
    http.expectOne('/api/v1/storage/assignments').flush([ASSIGN]);
    fixture.detectChanges();
    expect(text()).toContain('Assigned bulk to /mnt/data.');
  });

  it('confirms re-assignment with an impact message', () => {
    const other = { ...REG, Mountpoint: '/mnt/other', Label: 'other' };
    load([MOUNT], [REG, other], [ASSIGN]);
    fixture.componentInstance.assign('bulk', '/mnt/other', 'chris');
    fixture.detectChanges();
    http.expectNone('/api/v1/storage/assignments');
    expect(text()).toContain('bulk is currently on /mnt/data');
    expect(text()).toContain('installed apps keep using it');
    click('Confirm re-assign');
    http
      .expectOne('/api/v1/storage/assignments')
      .flush('storage: "/mnt/other" is not suitable for bulk', { status: 422, statusText: 'x' });
    fixture.detectChanges();
    expect(text()).toContain('is not suitable for bulk');
  });

  it('cancels a re-assignment', () => {
    const other = { ...REG, Mountpoint: '/mnt/other', Label: 'other' };
    load([MOUNT], [REG, other], [ASSIGN]);
    fixture.componentInstance.assign('bulk', '/mnt/other', 'chris');
    fixture.detectChanges();
    click('Cancel');
    expect(text()).not.toContain('currently on');
  });
});
