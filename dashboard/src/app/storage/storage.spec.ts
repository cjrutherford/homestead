import { describe, expect, it, beforeEach, afterEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient } from '@angular/common/http';
import {
  provideHttpClientTesting,
  HttpTestingController,
} from '@angular/common/http/testing';
import { StorageSetup, storageLabel } from './storage';

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
const RO = {
  ...MOUNT,
  ID: 2,
  Mountpoint: '/mnt/ro',
  ReadOnly: true,
  registrable: false,
  registrable_reason: 'storage: "/mnt/ro" is read-only',
};
const DEAD = {
  ...MOUNT,
  ID: 3,
  Mountpoint: '/mnt/dead',
  Accessible: false,
  registrable: false,
  registrable_reason: 'storage: "/mnt/dead" serves no usage data',
};
const TMP = {
  ...MOUNT,
  ID: 4,
  Mountpoint: '/run/tmp',
  FSType: 'tmpfs',
  registrable: false,
  registrable_reason: 'storage: "/run/tmp" is a tmpfs pseudo-filesystem',
};
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
    expect(fixture.nativeElement.querySelectorAll('app-loading[aria-busy="true"]').length).toBe(3);
    http.match(() => true).forEach((r) => r.flush([]));
  });

  it('shows empty states', () => {
    load([], [], []);
    expect(text()).toContain('No mounts discovered.');
    expect(text()).toContain('No mounts registered yet.');
    expect(text()).toContain('No classes assigned yet.');
  });

  it('lists mounts with capacity, access, read-only and registration', () => {
    load([MOUNT, RO, DEAD, TMP], [REG], [ASSIGN]);
    expect(text()).toContain('/mnt/data');
    expect(text()).toContain('1.0 TiB free of 2.0 TiB');
    expect(text()).toContain('Registered as main');
    expect(text()).toContain('holds bulk');
    expect(text()).toContain('Cannot be registered: "/mnt/ro" is read-only');
    expect(text()).toContain('tmpfs pseudo-filesystem');
    expect(text()).toContain('serves no usage data');
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
    c.register('', 'main');
    fixture.detectChanges();
    expect(text()).toContain('Enter the mount path');
    c.register('mnt/data', 'main');
    fixture.detectChanges();
    expect(text()).toContain('absolute path');
    c.register('/mnt/data', 'Main_1');
    fixture.detectChanges();
    expect(text()).toContain('Start the label');
  });

  it('registers a mount and reloads', () => {
    load([MOUNT], [], []);
    fixture.componentInstance.register(' /mnt/data ', ' main ');
    const req = http.expectOne('/api/v1/storage/registrations');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ mountpoint: '/mnt/data', label: 'main' });
    req.flush(REG);
    http.expectOne('/api/v1/storage/mounts').flush([MOUNT]);
    http.expectOne('/api/v1/storage/registrations').flush([REG]);
    http.expectOne('/api/v1/storage/assignments').flush([]);
    fixture.detectChanges();
    expect(text()).toContain('Registered /mnt/data as main.');
  });

  it('surfaces the daemon refusal for registration', () => {
    load([MOUNT], [], []);
    fixture.componentInstance.register('/mnt/data', 'main');
    http
      .expectOne('/api/v1/storage/registrations')
      .flush('storage: "/mnt/data" is read-only', { status: 422, statusText: 'x' });
    fixture.detectChanges();
    expect(text()).toContain('is read-only');
  });

  it('confirms before renaming an already registered mount', () => {
    load([MOUNT], [REG], []);
    fixture.componentInstance.register('/mnt/data', 'primary');
    fixture.detectChanges();
    http.expectNone('/api/v1/storage/registrations');
    expect(text()).toContain('is already registered. This changes');
    click('Cancel');
    expect(text()).not.toContain('is already registered. This changes');
    fixture.componentInstance.register('/mnt/data', 'primary');
    fixture.detectChanges();
    click('Confirm rename');
    http.expectOne('/api/v1/storage/registrations').flush({ ...REG, Label: 'primary' });
    http.match(() => true).forEach((r) => r.flush([]));
  });

  it('validates assignment input', () => {
    load([MOUNT], [REG], []);
    const c = fixture.componentInstance;
    c.assign('floppy', '/mnt/data');
    fixture.detectChanges();
    expect(text()).toContain('Choose a storage class');
    c.assign('bulk', '');
    fixture.detectChanges();
    expect(text()).toContain('Choose a registered mount');
  });

  it('assigns a class to a new mount without confirmation', () => {
    load([MOUNT], [REG], []);
    fixture.componentInstance.assign('bulk', '/mnt/data');
    const req = http.expectOne('/api/v1/storage/assignments');
    expect(req.request.method).toBe('POST');
    expect(req.request.body).toEqual({ class: 'bulk', mountpoint: '/mnt/data' });
    req.flush(ASSIGN);
    http.expectOne('/api/v1/storage/mounts').flush([MOUNT]);
    http.expectOne('/api/v1/storage/registrations').flush([REG]);
    http.expectOne('/api/v1/storage/assignments').flush([ASSIGN]);
    fixture.detectChanges();
    expect(text()).toContain('Assigned bulk to /mnt/data.');
  });

  const OTHER = { ...REG, Mountpoint: '/mnt/other', Label: 'other' };
  const IMPACT = {
    error: 'moving bulk affects installed packages',
    class: 'bulk',
    from: '/mnt/data',
    to: '/mnt/other',
    affected_packages: ['jellyfin', 'immich'],
  };

  function reassignToImpact() {
    load([MOUNT], [REG, OTHER], [ASSIGN]);
    fixture.componentInstance.assign('bulk', '/mnt/other');
    const first = http.expectOne('/api/v1/storage/assignments');
    expect(first.request.body.acknowledge_impact).toBeUndefined();
    first.flush(IMPACT, { status: 409, statusText: 'Conflict' });
    fixture.detectChanges();
  }

  it('lists affected packages on a 409 and acknowledges only after confirm', () => {
    reassignToImpact();
    expect(text()).toContain('jellyfin, immich');
    expect(text()).toContain('from /mnt/data to /mnt/other');
    http.expectNone('/api/v1/storage/assignments');
    click('Confirm re-assign');
    const second = http.expectOne('/api/v1/storage/assignments');
    expect(second.request.body).toEqual({
      class: 'bulk',
      mountpoint: '/mnt/other',
      acknowledge_impact: true,
    });
    second.flush({ ...ASSIGN, Mountpoint: '/mnt/other' });
    http.match(() => true).forEach((r) => r.flush([]));
  });

  it('shows the daemon refusal after an acknowledged re-assign fails', () => {
    reassignToImpact();
    click('Confirm re-assign');
    http
      .expectOne('/api/v1/storage/assignments')
      .flush('storage: "/mnt/other" is not suitable for bulk', { status: 422, statusText: 'x' });
    fixture.detectChanges();
    expect(text()).toContain('is not suitable for bulk');
  });

  it('cancels a re-assignment without acknowledging', () => {
    reassignToImpact();
    click('Cancel');
    expect(text()).not.toContain('affects these installed packages');
    http.expectNone('/api/v1/storage/assignments');
  });

  it('disables the forms while a confirmation is pending', () => {
    reassignToImpact();
    const q = (sel: string) => fixture.nativeElement.querySelector(sel);
    expect(q('select[name=class]').disabled).toBe(true);
    expect(q('select[name=assign-mountpoint]').disabled).toBe(true);
  });

  it('re-prompts rather than posting when rename values change', () => {
    load([MOUNT], [REG], []);
    const c = fixture.componentInstance;
    c.register('/mnt/data', 'primary');
    c.register('/mnt/data', 'other-name');
    fixture.detectChanges();
    http.expectNone('/api/v1/storage/registrations');
    expect(text()).toContain('label to other-name');
    click('Confirm rename');
    const req = http.expectOne('/api/v1/storage/registrations');
    expect(req.request.body.label).toBe('other-name');
    req.flush({ ...REG, Label: 'other-name' });
    http.match(() => true).forEach((r) => r.flush([]));
  });

  it('says the daemon is unreachable instead of [object ProgressEvent]', () => {
    fixture.detectChanges();
    http.expectOne('/api/v1/storage/mounts').error(new ProgressEvent('error'));
    http.expectOne('/api/v1/storage/registrations').flush([]);
    http.expectOne('/api/v1/storage/assignments').flush([]);
    fixture.detectChanges();
    expect(text()).toContain("Can't reach the appliance");
    expect(text()).not.toContain('ProgressEvent');
  });

  it('moves focus into the dialog, closes on Escape, and returns focus', async () => {
    document.body.appendChild(fixture.nativeElement);
    load([MOUNT], [REG], []);
    const submit = fixture.nativeElement.querySelector(
      'form button[type=submit]',
    ) as HTMLButtonElement;
    const opener = Array.from(
      fixture.nativeElement.querySelectorAll('button') as NodeListOf<HTMLButtonElement>,
    ).find((b) => b.textContent?.trim() === 'Register mount')!;
    expect(submit).toBeTruthy();
    opener.focus();
    fixture.componentInstance.register('/mnt/data', 'primary');
    fixture.detectChanges();
    await Promise.resolve();
    const dialog = fixture.nativeElement.querySelector('[role=alertdialog]') as HTMLElement;
    expect(document.activeElement).toBe(dialog);
    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[role=alertdialog]')).toBeNull();
    await new Promise((resolve) => setTimeout(resolve));
    expect(document.activeElement).toBe(opener);
    fixture.nativeElement.remove();
  });

  it('shows registration problems next to each field', () => {
    load([MOUNT], [], []);
    const el = fixture.nativeElement as HTMLElement;
    fixture.componentInstance.register('', 'Bad_1');
    fixture.detectChanges();
    const mount = el.querySelector('#reg-mount') as HTMLInputElement;
    expect(mount.getAttribute('aria-invalid')).toBe('true');
    expect(mount.getAttribute('aria-describedby')).toBe('reg-mount-error');
    expect(el.querySelector('#reg-mount-error')?.textContent).toContain('Enter the mount path');
    expect(el.querySelector('#reg-label-error')?.textContent).toContain('Start the label');
    expect(el.querySelectorAll('.field-error').length).toBe(2);
  });

  it('lists a mount once when discovery returns it twice', () => {
    load([MOUNT, { ...MOUNT, ID: 9 }], [], []);
    expect(fixture.nativeElement.querySelectorAll('table[aria-label="Discovered mounts"] tbody tr').length).toBe(1);
  });

  describe('unassign and unregister', () => {
    beforeEach(() => {
      HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) { this.setAttribute('open', ''); };
    });
    const dialog = () => (fixture.nativeElement as HTMLElement).querySelector('dialog');

    it('asks before unassigning, then calls the daemon and refreshes', () => {
      load([MOUNT], [REG], [ASSIGN]);
      click('Unassign');
      expect(dialog()).not.toBeNull();
      expect(text()).toContain('Nothing on the disk is deleted');
      expect(text()).toContain('even when another class shares');
      http.expectNone('/api/v1/storage/assignments/bulk');
      click('Confirm unassign');
      const req = http.expectOne('/api/v1/storage/assignments/bulk');
      expect(req.request.method).toBe('DELETE');
      req.flush(null, { status: 204, statusText: 'No Content' });
      http.expectOne('/api/v1/storage/mounts').flush([MOUNT]);
      http.expectOne('/api/v1/storage/registrations').flush([REG]);
      http.expectOne('/api/v1/storage/assignments').flush([]);
      fixture.detectChanges();
      expect(text()).toContain('Unassigned bulk');
    });

    it('cancelling the dialog sends nothing', () => {
      load([MOUNT], [REG], [ASSIGN]);
      click('Unregister');
      expect(text()).toContain('still holds bulk');
      click('Cancel');
      expect(dialog()).toBeNull();
    });

    it('shows the daemon refusal naming the apps', () => {
      load([MOUNT], [REG], []);
      click('Unregister');
      click('Confirm unregister');
      const req = http.expectOne((r) => r.url === '/api/v1/storage/registrations' && r.method === 'DELETE');
      expect(req.request.params.get('mountpoint')).toBe('/mnt/data');
      req.flush(
        { error: 'cannot unregister /mnt/data: installed apps keep data there (jellyfin)', affected_packages: ['jellyfin'] },
        { status: 409, statusText: 'Conflict' },
      );
      fixture.detectChanges();
      expect(text()).toContain('jellyfin');
    });
  });
  describe('S293 layout help', () => {
    const APP = { ...MOUNT, ID: 10, Mountpoint: '/mnt/app', Label: '', TotalBytes: 100 * 1024 ** 3, kind: 'data', suggested: ['application', 'database'] };
    const BULK = { ...MOUNT, ID: 11, Mountpoint: '/mnt/bulk', Label: 'Media Disk', kind: 'data', suggested: ['bulk'] };
    const SANDBOX = { ...MOUNT, ID: 12, Device: '/dev/vda2', Mountpoint: '/var/lib/homestead/sandbox', kind: 'system', suggested: [] };
    const PROC = { ...TMP, ID: 13, kind: 'unusable', suggested: [] };

    it('lists data disks first and folds the rest away', () => {
      load([SANDBOX, APP, PROC], [], []);
      const rows = (label: string) =>
        Array.from(
          fixture.nativeElement.querySelectorAll(`table[aria-label="${label}"] tbody tr`) as NodeListOf<HTMLElement>,
        ).map((row) => row.querySelector('strong')?.textContent);
      expect(rows('Discovered mounts')).toEqual(['/mnt/app']);
      expect(rows('Other mounts')).toEqual(['/var/lib/homestead/sandbox', '/run/tmp']);
      expect(fixture.nativeElement.querySelector('details:has(table[aria-label="Other mounts"]) summary').textContent).toContain('Other mounts (2)');
    });

    it('explains the classes and drops the acting owner fields', () => {
      load([APP], [], []);
      expect(text()).toContain('What the classes mean');
      expect(text()).toContain('large media and libraries');
      expect(text()).toContain('Apps that need bulk or backup storage ask which registered mount');
      expect(text()).toContain('Not assigned yet: application, database, bulk, backup, cache.');
      expect(fixture.nativeElement.querySelector('#reg-actor')).toBeNull();
      expect(fixture.nativeElement.querySelector('input[name=assign-actor]')).toBeNull();
      expect(text()).toContain('application (not assigned)');
    });

    it('applies the suggested layout: register, then assign, one at a time', () => {
      load([APP, BULK, SANDBOX], [{ ...REG, Mountpoint: '/mnt/bulk', Label: 'media' }], []);
      expect(text()).toContain('Suggested layout');
      expect(text()).toContain('application, database');
      click('Use the suggested layout');
      const reg = http.expectOne('/api/v1/storage/registrations');
      expect(reg.request.body).toEqual({ mountpoint: '/mnt/app', label: 'app' });
      reg.flush(REG);
      const bodies: unknown[] = [];
      for (let i = 0; i < 3; i++) {
        const req = http.expectOne('/api/v1/storage/assignments');
        bodies.push(req.request.body);
        req.flush(ASSIGN);
      }
      expect(bodies).toEqual([
        { class: 'application', mountpoint: '/mnt/app' },
        { class: 'database', mountpoint: '/mnt/app' },
        { class: 'bulk', mountpoint: '/mnt/bulk' },
      ]);
      http.expectOne('/api/v1/storage/mounts').flush([]);
      http.expectOne('/api/v1/storage/registrations').flush([]);
      http.expectOne('/api/v1/storage/assignments').flush([]);
      fixture.detectChanges();
      expect(text()).toContain('Applied the suggested layout.');
    });

    it('stops the layout at the first refusal and says so', () => {
      load([APP], [], []);
      click('Use the suggested layout');
      http
        .expectOne('/api/v1/storage/registrations')
        .flush('storage: "/mnt/app" is read-only', { status: 422, statusText: 'x' });
      http.expectOne('/api/v1/storage/mounts').flush([APP]);
      http.expectOne('/api/v1/storage/registrations').flush([]);
      http.expectOne('/api/v1/storage/assignments').flush([]);
      http.expectNone('/api/v1/storage/assignments');
      fixture.detectChanges();
      expect(text()).toContain('stopped part way: storage: "/mnt/app" is read-only');
    });

    it('hides the suggestion when nothing is proposed', () => {
      load([{ ...APP, suggested: [] }], [], []);
      expect(text()).not.toContain('Suggested layout');
    });

    it('derives registration labels from the disk label or path', () => {
      const taken = new Set(['media-disk']);
      expect(storageLabel(BULK as never, taken)).toBe('media-disk-2');
      expect(storageLabel(APP as never, new Set())).toBe('app');
      expect(storageLabel({ ...APP, Mountpoint: '/srv/2024_Photos!' } as never, new Set())).toBe('photos');
      expect(storageLabel({ ...APP, Mountpoint: '/' } as never, new Set())).toBe('disk');
      expect(storageLabel({ ...APP, Label: 'x'.repeat(40) } as never, new Set(['x'.repeat(32)]))).toBe('x'.repeat(30) + '-2');
    });
  });
});
