import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, convertToParamMap, provideRouter } from '@angular/router';
import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { Subject, of } from 'rxjs';
import { ApiService, OperationState } from '../core/api.service';
import { workOrderInterceptor } from '../core/work-order.interceptor';
import { WorkOrders } from '../core/work-orders.service';
import { InstallWizard } from './install-wizard';

const CRAFT = {
  Name: 'gridcraft',
  Version: '0.7.0',
  Title: 'GridCraft',
  Blurb: 'Spreadsheet that runs in your browser.',
  Description: 'GridCraft is a spreadsheet.',
  Category: 'productivity',
  Services: [
    {
      Name: 'gridcraft',
      Image: 'homestead/craft-gridcraft:0.7.0',
      Exposes: 'web',
      Data: [{ Target: '/tmp', Class: '', Subpath: '', Ephemeral: true }],
    },
  ],
  Requirements: { MemoryMB: 64, Storage: null, Ports: null },
  Exposure: {
    Package: 'gridcraft',
    Endpoints: [{ Endpoint: 'web', Recommended: 'local', AllowWider: false, Warnings: ['Keep it local.'] }],
  },
  BackupPaths: [],
  SSO: { Class: 'unsupported' },
};

const NEXTCLOUD = {
  Name: 'nextcloud',
  Version: '35.0.1',
  Title: 'Nextcloud',
  Blurb: 'Files you host.',
  Description: 'Nextcloud keeps files in sync.',
  Category: 'files',
  Services: [
    {
      Name: 'nextcloud',
      Image: 'nextcloud:35.0.1-apache',
      Exposes: 'web',
      Data: [
        { Target: '/var/www/html', Class: 'application', Subpath: 'html' },
        { Target: '/var/www/html/data', Class: 'bulk', Subpath: 'data' },
      ],
    },
  ],
  Requirements: {
    MemoryMB: 1024,
    Storage: [
      { Class: 'application', MinBytes: 2147483648 },
      { Class: 'bulk', MinBytes: 53687091200 },
    ],
    Ports: [80],
  },
  Exposure: {
    Package: 'nextcloud',
    Endpoints: [{ Endpoint: 'web', Recommended: 'local', AllowWider: true, Warnings: [] }],
  },
  BackupPaths: ['config'],
  SSO: { Class: 'optional', Protocols: ['oidc', 'saml'] },
};

const NEXTCLOUD_NO_PORTS = { ...NEXTCLOUD, Requirements: { ...NEXTCLOUD.Requirements, Ports: null } };

const AUTHENTIK = {
  ...NEXTCLOUD,
  Name: 'authentik',
  Title: 'Authentik',
  Services: [
    {
      Name: 'server',
      Image: 'ghcr.io/goauthentik/server:2026.8.3',
      Exposes: 'web',
      Data: [{ Target: '/media', Class: 'application', Subpath: 'media' }],
    },
  ],
  Requirements: { MemoryMB: 2048, Storage: [{ Class: 'application', MinBytes: 2147483648 }], Ports: [9000] },
  SSO: { Class: 'unsupported' },
  Secrets: [
    { Name: 'db_password', Immutable: false },
    { Name: 'secret_key', Immutable: true },
  ],
};

const PACKAGES = (installed: string[] = []) =>
  [CRAFT, NEXTCLOUD, AUTHENTIK].map((m) => ({
    name: m.Name,
    version: m.Version,
    title: m.Title,
    blurb: m.Blurb,
    category: m.Category,
    icon: 'x',
    installed: installed.includes(m.Name),
  }));

const GiB = 1024 ** 3;
const REGISTRATIONS = [
  { Mountpoint: '/mnt/small', Label: 'small', Actor: 'owner', AddedAt: '' },
  { Mountpoint: '/mnt/big', Label: 'big', Actor: 'owner', AddedAt: '' },
];
const MOUNTS = [
  { Mountpoint: '/mnt/small', Accessible: true, ReadOnly: false, FreeBytes: 1 * GiB, AvailBytes: 1 * GiB, TotalBytes: 10 * GiB },
  { Mountpoint: '/mnt/big', Accessible: true, ReadOnly: false, FreeBytes: 500 * GiB, AvailBytes: 500 * GiB, TotalBytes: 1000 * GiB },
  { Mountpoint: '/mnt/unregistered', Accessible: true, ReadOnly: false, FreeBytes: 9 * GiB, AvailBytes: 9 * GiB, TotalBytes: 9 * GiB },
];

function plan(name: string, extra: Record<string, unknown> = {}) {
  return {
    Package: name,
    Requirements: { Passed: true },
    Routes: [{ Host: `${name}.lan`, Exposure: 'local', Target: { Host: name, Port: 8080 }, TLS: 'internal' }],
    Compose: { ComposeYAML: `name: ${name}` },
    DNS: [],
    TLS: [{ Host: `${name}.lan`, Issuer: 'internal' }],
    Firewall: [{ Service: { Name: 'web-https' } }],
    Directories: [],
    Mounts: [],
    Notes: ['install creates the homestead-edge network if it is missing'],
    Actions: ['provision', 'up'],
    EdgeNetwork: true,
    ...extra,
  };
}

function opState(id: number, status: string, error = ''): OperationState {
  return {
    id,
    type: 'catalog.install',
    status,
    payload: '',
    result: '',
    error,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    steps: [
      { id: 1, operation_id: id, position: 0, name: 'ensure-homestead-edge', status: status === 'succeeded' ? 'succeeded' : 'running', started_at: '', finished_at: '', error: '' },
      { id: 2, operation_id: id, position: 1, name: 'up', status: status === 'succeeded' ? 'succeeded' : status === 'failed' ? 'failed' : 'pending', started_at: '', finished_at: '', error: '' },
    ],
  };
}

const HEALTH_WITH_CADDY = {
  reachable: true,
  version: '26.1',
  containers: [{ names: ['/homestead-caddy'], state: 'running' }],
  networks: [{ name: 'homestead-edge' }],
};
const HEALTH_NO_CADDY = { reachable: true, version: '26.1', containers: [], networks: [] };

describe('InstallWizard', () => {
  let fixture: ComponentFixture<InstallWizard>;
  let http: HttpTestingController;
  let streams: Map<number, Subject<OperationState>>;

  // The component's protected members are the wizard's own surface in these specs.
  const c = () => fixture.componentInstance as any;
  const text = () => (fixture.nativeElement as HTMLElement).textContent!.replace(/\s+/g, ' ');
  const button = (label: string) =>
    Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('button, a.button')).find(
      (b) => b.textContent!.trim() === label,
    ) as HTMLButtonElement | undefined;

  function start(
    manifest: Record<string, unknown>,
    options: {
      installed?: string[];
      health?: unknown;
      storage?: boolean;
      craft?: unknown;
      mounts?: unknown[];
      assignments?: unknown[];
      inputs?: Record<string, unknown>;
    } = {},
  ) {
    const name = manifest['Name'] as string;
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        provideHttpClient(withInterceptors([workOrderInterceptor])),
        provideHttpClientTesting(),
        { provide: ActivatedRoute, useValue: { paramMap: of(convertToParamMap({ name })) } },
      ],
    });
    streams = new Map();
    vi.spyOn(TestBed.inject(ApiService), 'operationEvents').mockImplementation((id: number) => {
      const subject = new Subject<OperationState>();
      streams.set(id, subject);
      return subject;
    });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(InstallWizard);
    for (const [key, value] of Object.entries(options.inputs ?? {})) {
      fixture.componentRef.setInput(key, value);
    }
    fixture.detectChanges();
    http.expectOne(`/api/v1/catalog/${name}`).flush(manifest);
    http.expectOne('/api/v1/catalog').flush(PACKAGES(options.installed));
    // An installed package opens on Done, which asks for the health once more.
    for (const request of http.match('/api/v1/docker/health')) {
      request.flush(options.health ?? HEALTH_WITH_CADDY);
    }
    if (options.storage ?? (manifest['Requirements'] as { Storage: unknown })['Storage'] !== null) {
      http.expectOne('/api/v1/storage/registrations').flush(REGISTRATIONS);
      http.expectOne('/api/v1/storage/mounts').flush(options.mounts ?? MOUNTS);
      http.expectOne('/api/v1/storage/assignments').flush(options.assignments ?? []);
    }
    if (name === 'gridcraft') {
      http.expectOne('/api/v1/craft').flush(options.craft ?? { images: [], verdicts: [], baselines: {} });
    }
    fixture.detectChanges();
  }

  /** Goes to a step the way the wizard does, with its checks. */
  function to(id: string) {
    const index = c().steps().findIndex((step: { id: string }) => step.id === id);
    c().go(index);
    fixture.detectChanges();
  }

  /** The Done step reads the route table and the edge health. */
  function doneReads(routes: unknown[], health: object = HEALTH_WITH_CADDY) {
    http.expectOne('/api/v1/edge/routes').flush({ routes });
    for (const request of http.match('/api/v1/docker/health')) {
      request.flush(health);
    }
    fixture.detectChanges();
  }

  beforeEach(() => {
    // The Done step asks this device to open the address; no network in specs.
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(''))));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    http.verify();
  });

  it('shows the steps of a stateless craft app without storage or sign-in', () => {
    start(CRAFT);
    expect(c().steps().map((s: { id: string }) => s.id)).toEqual(['about', 'preflight', 'address', 'review', 'install', 'done']);
    expect(text()).toContain('Install GridCraft');
    expect(text()).toContain('What GridCraft is');
    expect(text()).toContain('Here is what GridCraft is');
    expect(text()).toContain('Spreadsheet that runs in your browser.');
  });

  it('shows storage and sign-in steps for a package with storage and optional sso', () => {
    start(NEXTCLOUD);
    expect(c().steps().map((s: { id: string }) => s.id)).toEqual([
      'about', 'preflight', 'storage', 'address', 'sign-in', 'review', 'install', 'done',
    ]);
  });

  it('puts focus on the step heading and announces the step when it changes', () => {
    start(CRAFT);
    const heading = (fixture.nativeElement as HTMLElement).querySelector('h2')!;
    expect(heading.getAttribute('tabindex')).toBe('-1');
    c().go(1);
    http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft'));
    fixture.detectChanges();
    expect(c().toast.announcement().trim()).toBe('Step 2 of 6: Requirements');
  });

  describe('preflight', () => {
    it('asks the daemon for the plan and passes a package that meets the requirements', () => {
      start(CRAFT);
      to('preflight');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft'));
      fixture.detectChanges();
      expect(text()).toContain('This appliance meets the requirements.');
      const caddy = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('.wizard-checks li')).find((li) =>
        li.textContent!.includes('Caddy edge proxy'),
      )!;
      expect(caddy.querySelector('.badge')!.textContent).toBe('Running');
      c().next();
      expect(c().current()).toBe('address');
    });

    it('does not advance past a failing hard requirement and says how to fix it', () => {
      start(CRAFT);
      to('preflight');
      http
        .expectOne('/api/v1/catalog/gridcraft/plan')
        .flush('catalog: requirements unmet for "gridcraft": memory: host offers 32 MB for 64 required', { status: 422, statusText: 'Unprocessable' });
      fixture.detectChanges();
      expect(text()).toContain('memory: host offers 32 MB for 64 required');
      expect(text()).toContain('less memory');
      c().next();
      fixture.detectChanges();
      expect(c().current()).toBe('preflight');
      expect(text()).toContain('does not meet a requirement');
    });

    it('blocks when Docker is not reachable', () => {
      start(CRAFT, { health: { reachable: false } });
      to('preflight');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft'));
      fixture.detectChanges();
      expect(text()).toContain('Not reachable');
      c().next();
      expect(c().current()).toBe('preflight');
      expect(c().stepProblem()).toContain('Docker is not reachable');
    });

    it('warns that routes will not serve without Caddy, links to the Edge page, and lets the owner acknowledge', () => {
      start(CRAFT, { health: HEALTH_NO_CADDY });
      to('preflight');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft'));
      fixture.detectChanges();
      expect(text()).toContain('The edge (Caddy');
      expect(text()).toContain('will not be reachable until it is');
      expect(button('Set up the edge')).toBeDefined();
      expect(text()).toContain('Created on install');
      c().next();
      expect(c().current()).toBe('preflight');
      expect(c().stepProblem()).toContain('Caddy is not running');
      c().acknowledgedEdge.set(true);
      c().next();
      expect(c().current()).toBe('address');
    });

    it('offers to set up the edge behind a confirm, deploys Caddy and shows it in the rail', () => {
      start(CRAFT, { health: HEALTH_NO_CADDY });
      to('preflight');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft'));
      fixture.detectChanges();
      button('Set up the edge')!.click();
      fixture.detectChanges();
      const dialog = (fixture.nativeElement as HTMLElement).querySelector('app-dialog')!;
      expect(dialog.textContent).toContain('ports');
      expect(dialog.textContent).toContain('80 and 443');
      expect(dialog.textContent).toContain('does not open anything to the internet');
      http.expectNone('/api/v1/edge/deploy');
      (Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent!.trim() === 'Set up the edge') as HTMLButtonElement).click();
      http.expectOne('/api/v1/edge/deploy').flush({ id: 21 });
      fixture.detectChanges();
      expect(TestBed.inject(WorkOrders).orders().length).toBe(1);
      expect((fixture.nativeElement as HTMLElement).querySelector('app-operation-progress a[href="/operations/21"]')).not.toBeNull();
      streams.get(21)!.next(opState(21, 'succeeded'));
      http.expectOne('/api/v1/docker/health').flush(HEALTH_WITH_CADDY);
      fixture.detectChanges();
      expect(text()).toContain('The edge is set up.');
      expect(c().caddyMissing()).toBe(false);
      c().next();
      expect(c().current()).toBe('address');
    });

    it('lets the owner decline the edge and carry on', () => {
      start(CRAFT, { health: HEALTH_NO_CADDY });
      to('preflight');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft'));
      fixture.detectChanges();
      button('Set up the edge')!.click();
      fixture.detectChanges();
      (Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('app-dialog button')).find((b) => b.textContent!.trim() === 'Not now') as HTMLButtonElement).click();
      fixture.detectChanges();
      expect((fixture.nativeElement as HTMLElement).querySelector('app-dialog')).toBeNull();
      c().acknowledgedEdge.set(true);
      c().next();
      expect(c().current()).toBe('address');
    });

    it('defers the plan check until storage is chosen', () => {
      start(NEXTCLOUD);
      to('preflight');
      http.expectNone('/api/v1/catalog/nextcloud/plan');
      expect(text()).toContain('checked once you choose storage');
    });

    it('sends an installed package to the catalog instead of a retry', () => {
      start(CRAFT);
      to('preflight');
      http
        .expectOne('/api/v1/catalog/gridcraft/plan')
        .flush('catalog: "gridcraft" is already installed; use configure or update instead of installing again', { status: 409, statusText: 'Conflict' });
      fixture.detectChanges();
      expect(c().planProblem().installed).toBe(true);
      expect(text()).toContain('Back to the catalog');
    });
  });

  describe('storage', () => {
    it('lists only registered mounts with their free space and the owner-visible paths', () => {
      start(NEXTCLOUD);
      to('storage');
      const options = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('option')).map((o) => o.textContent!.trim());
      expect(options).toContain('/mnt/big, 500.0 GiB free');
      expect(options).toContain('/mnt/small, 1.0 GiB free');
      expect(options.join(' ')).not.toContain('unregistered');
      c().setMount('application', '/mnt/big');
      c().setMount('bulk', '/mnt/big');
      fixture.detectChanges();
      expect(text()).toContain('/mnt/big/nextcloud/html');
      expect(text()).toContain('/mnt/big/nextcloud/data');
    });

    it('offers a mount once even when the appliance lists it twice', () => {
      start(NEXTCLOUD, { mounts: [...MOUNTS, MOUNTS[1]] });
      to('storage');
      const options = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('select[name="mount-bulk"] option')).map((o) => o.textContent!.trim());
      expect(options.filter((o) => o.startsWith('/mnt/big'))).toHaveLength(1);
    });

    it('defaults the container port to 80 for Nextcloud, which declares no host port', () => {
      start(NEXTCLOUD_NO_PORTS);
      expect(c().containerPorts()['nextcloud']).toBe('80');
    });

    it('will not continue until each kind of storage has a mount with enough room', () => {
      start(NEXTCLOUD);
      to('storage');
      c().next();
      expect(c().current()).toBe('storage');
      expect(c().stepProblem()).toContain('Choose where app data goes');
      c().setMount('application', '/mnt/big');
      c().setMount('bulk', '/mnt/small');
      c().next();
      expect(c().current()).toBe('storage');
      expect(c().stepProblem()).toContain('enough free space');
      fixture.detectChanges();
      const field = (fixture.nativeElement as HTMLElement).querySelector('select[name="mount-bulk"]')!;
      expect(field.getAttribute('aria-invalid')).toBe('true');
      const error = (fixture.nativeElement as HTMLElement).querySelector(`#${field.getAttribute('aria-describedby')}`)!;
      expect(error.textContent).toContain('/mnt/small has 1.0 GiB free');
    });

    it('re-plans with the chosen mounts and moves on when the plan is accepted', () => {
      start(NEXTCLOUD);
      to('storage');
      c().setMount('application', '/mnt/big');
      c().setMount('bulk', '/mnt/big');
      c().next();
      const request = http.expectOne('/api/v1/catalog/nextcloud/plan');
      expect(request.request.body.storage).toEqual({ application: '/mnt/big', bulk: '/mnt/big' });
      expect(request.request.body.container_ports).toEqual({ nextcloud: 80 });
      request.flush(plan('nextcloud'));
      expect(c().current()).toBe('address');
    });

    it('keeps the owner on the step when the daemon refuses the storage', () => {
      start(NEXTCLOUD);
      to('storage');
      c().setMount('application', '/mnt/big');
      c().setMount('bulk', '/mnt/big');
      c().next();
      http
        .expectOne('/api/v1/catalog/nextcloud/plan')
        .flush('/mnt/big is not a mounted filesystem; mount the storage before installing', { status: 422, statusText: 'x' });
      expect(c().current()).toBe('storage');
      expect(c().stepProblem()).toContain('not a mounted filesystem');
    });

    it('sends an existing folder as a host path and keeps entries across Back and Next', () => {
      start(NEXTCLOUD);
      to('storage');
      c().addFolder();
      c().patchFolder(0, { path: '/mnt/big/media', target: '/media' });
      c().setMount('application', '/mnt/big');
      c().setMount('bulk', '/mnt/big');
      to('address');
      to('storage');
      expect(c().folders()).toHaveLength(1);
      expect(c().mountpoints().bulk).toBe('/mnt/big');
      to('review');
      const request = http.expectOne('/api/v1/catalog/nextcloud/plan');
      expect(request.request.body.volumes).toEqual([
        { service: 'nextcloud', class: 'bulk', hostpath: '/mnt/big/media', target: '/media', readonly: true },
      ]);
      request.flush(plan('nextcloud'));
    });

    it('offers the Storage page when nothing is registered', () => {
      start(NEXTCLOUD);
      c().mountOptions.set([]);
      to('storage');
      expect(text()).toContain('No storage is registered yet.');
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/storage"]')).not.toBeNull();
      c().next();
      expect(c().current()).toBe('storage');
    });
  });

  describe('address and sign-in', () => {
    it('ties a missing domain to its field once the owner presses Continue', () => {
      start(CRAFT);
      to('address');
      c().domain.set('');
      fixture.detectChanges();
      expect((fixture.nativeElement as HTMLElement).querySelector('#domain-error')).toBeNull();
      c().next();
      fixture.detectChanges();
      const input = (fixture.nativeElement as HTMLElement).querySelector('input[name="domain"]')!;
      expect(input.getAttribute('aria-invalid')).toBe('true');
      expect(input.getAttribute('aria-describedby')).toContain('domain-error');
      expect((fixture.nativeElement as HTMLElement).querySelector('#domain-error')?.textContent).toContain('Enter a domain');
      expect(c().current()).toBe('address');
    });

    it('sends zone, public address and extra networks only when the owner fills them in', () => {
      start(CRAFT);
      c().zone.set('example.org');
      c().publicIp.set('203.0.113.7');
      c().extraNetworks.set('monitoring, backend');
      to('review');
      const request = http.expectOne('/api/v1/catalog/gridcraft/plan');
      expect(request.request.body.zone).toBe('example.org');
      expect(request.request.body.public_ip).toBe('203.0.113.7');
      expect(request.request.body.networks).toEqual([
        { name: 'monitoring', external: true },
        { name: 'backend', external: true },
      ]);
      request.flush(plan('gridcraft'));
    });

    it('defaults the name to the app on the lan domain and never turns on public exposure', () => {
      start(CRAFT);
      to('address');
      const host = (fixture.nativeElement as HTMLElement).querySelector('input[name="host-web"]') as HTMLInputElement;
      expect(host.value).toBe('gridcraft.lan');
      expect(text()).toContain('Local network only');
      expect(text()).toContain('This app cannot be widened.');
      expect(text()).toContain('Installing never opens GridCraft to anyone outside your network');
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/exposure"]')).not.toBeNull();
    });

    it('sends a changed hostname as an override', () => {
      start(CRAFT);
      c().setHostname('web', 'sheets.home.arpa');
      to('review');
      const request = http.expectOne('/api/v1/catalog/gridcraft/plan');
      expect(request.request.body.hostnames).toEqual({ web: 'sheets.home.arpa' });
      expect(request.request.body.container_ports).toEqual({ gridcraft: 8080 });
      request.flush(plan('gridcraft'));
    });

    it('explains an optional sso package and names its protocols', () => {
      start(NEXTCLOUD);
      to('sign-in');
      expect(text()).toContain('can also sign people in through a sign-in service');
      expect(text()).toContain('OIDC, SAML');
      expect(text()).not.toContain('Passwords Homestead creates');
    });

    it('explains the sign-in service itself and lists generated secrets without values', () => {
      start(AUTHENTIK);
      to('sign-in');
      expect(text()).toContain('This is the sign-in service.');
      expect(text()).toContain('Passwords Homestead creates');
      expect(text()).toContain('db_password');
      expect(text()).toContain('It cannot be changed after install.');
      expect(text()).toContain('No value is shown or logged.');
      expect((fixture.nativeElement as HTMLElement).querySelector('input[type="password"]')).toBeNull();
    });
  });

  describe('review and install', () => {
    function review() {
      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(
        plan('gridcraft', { BrowserAdvisories: [{ Capability: 'webgl2', Label: 'WebGL2', Reason: 'Needs WebGL2.' }] }),
      );
      fixture.detectChanges();
    }

    it('plans again every time the review step is entered, so the plan shown is the one applied', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] } });
      review();
      c().back();
      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { Notes: [] }));
      fixture.detectChanges();
    });

    it('renders the plan in plain language with the raw plan expandable', () => {
      start(CRAFT);
      review();
      expect(text()).toContain('Homestead will create');
      expect(text()).toContain('Address gridcraft.lan (local network only)');
      expect(text()).toContain('Certificate for gridcraft.lan from your Homestead internal CA');
      expect(text()).toContain('Homestead will change');
      expect(text()).toContain('Firewall rule for web-https');
      expect(text()).toContain('Homestead will leave alone');
      expect(text()).toContain('merged into the route table');
      expect(text()).toContain('WebGL2');
      expect(text()).toContain('install creates the homestead-edge network');
      const raw = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('details')).find((d) =>
        d.textContent!.includes('Raw plan'),
      )!;
      expect(raw.querySelector('pre')!.textContent).toContain('"Package": "gridcraft"');
    });

    it('shows a refused plan with a fix and disables install', () => {
      start(CRAFT);
      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush('catalog: bad hostname', { status: 422, statusText: 'x' });
      fixture.detectChanges();
      expect(text()).toContain('bad hostname');
      expect(button('Verify and build GridCraft')!.disabled).toBe(true);
    });

    it('starts the install, joins the rail and ends on the done step', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] } });
      review();
      expect(button('Install GridCraft')).toBeDefined();
      button('Install GridCraft')!.click();
      const install = http.expectOne('/api/v1/catalog/gridcraft/install');
      expect(install.request.body.domain).toBe('lan');
      install.flush({ id: 7 });
      fixture.detectChanges();
      expect(c().current()).toBe('install');
      const rail = TestBed.inject(WorkOrders);
      expect(rail.orders().map((o) => o.title)).toEqual(['Install GridCraft']);
      expect(rail.orders()[0].done).toBe('GridCraft installed');
      expect((fixture.nativeElement as HTMLElement).querySelector('app-operation-progress a[href="/operations/7"]')).not.toBeNull();

      streams.get(7)!.next(opState(7, 'running'));
      fixture.detectChanges();
      expect(c().current()).toBe('install');
      streams.get(7)!.next(opState(7, 'succeeded'));
      const other = { Host: 'notes.lan', Target: { Host: 'notes', Port: 3000 }, Exposure: 'local', TLS: 'internal', Health: { Path: '', IntervalSeconds: 0 } };
      doneReads([other]);
      // The planned route is merged into the table; the existing route stays.
      const reconcile = http.expectOne('/api/v1/edge/routes');
      expect(reconcile.request.method).toBe('POST');
      expect(reconcile.request.body.routes.map((r: { Host: string }) => r.Host)).toEqual(['notes.lan', 'gridcraft.lan']);
      reconcile.flush({ id: 31 });
      fixture.detectChanges();
      expect(c().current()).toBe('done');
      expect(text()).toContain('Giving GridCraft an address');
      streams.get(31)!.next(opState(31, 'succeeded'));
      fixture.detectChanges();
      expect(text()).toContain('GridCraft installed');
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="https://gridcraft.lan"]')?.textContent).toContain('Open GridCraft');
      expect(text()).toContain('https://gridcraft.lan');
      expect(text()).toContain('There is no account to create.');
      expect(text()).toContain('GridCraft keeps nothing on the appliance');
      expect(c().reach()).toBe('checking');
    });

    it('says the app is not reachable and links to Trust when this device cannot open the address', async () => {
      vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('cert'))));
      start(CRAFT, { installed: ['gridcraft'] });
      doneReads([{ Host: 'gridcraft.lan', Target: { Host: 'gridcraft', Port: 8080 }, Exposure: 'local', TLS: 'internal', CraftApp: 'gridcraft' }]);
      await Promise.resolve();
      await Promise.resolve();
      fixture.detectChanges();
      expect(text()).toContain('could not open that address securely');
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/trust"]')).not.toBeNull();
    });

    it('offers the usual address for an installed app that has none, merged into the table', () => {
      start(CRAFT, { installed: ['gridcraft'] });
      const other = { Host: 'notes.lan', Target: { Host: 'notes', Port: 3000 }, Exposure: 'local', TLS: 'internal', Health: { Path: '', IntervalSeconds: 0 } };
      doneReads([other]);
      expect(text()).toContain('GridCraft has no address yet.');
      button('Give GridCraft an address')!.click();
      doneReads([other]);
      const reconcile = http.expectOne('/api/v1/edge/routes');
      expect(reconcile.request.method).toBe('POST');
      const sent = reconcile.request.body.routes;
      expect(sent.map((r: { Host: string }) => r.Host)).toEqual(['notes.lan', 'gridcraft.lan']);
      expect(sent[1]).toMatchObject({ Target: { Host: 'gridcraft', Port: 8080 }, Exposure: 'local', TLS: 'internal', CraftApp: 'gridcraft' });
      reconcile.flush({ id: 40 });
    });

    it('does not add a route when the edge is not running and says so, offering to set it up', () => {
      start(CRAFT, { installed: ['gridcraft'], health: HEALTH_NO_CADDY });
      doneReads([], HEALTH_NO_CADDY);
      expect(text()).toContain('GridCraft will not be reachable until the edge is set up.');
      expect(button('Set up the edge')).toBeDefined();
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/edge"]')).not.toBeNull();
    });

    it('says plainly when the edge refuses the new route and lets the owner retry', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] } });
      review();
      button('Install GridCraft')!.click();
      http.expectOne('/api/v1/catalog/gridcraft/install').flush({ id: 7 });
      streams.get(7)!.next(opState(7, 'succeeded'));
      doneReads([]);
      http.expectOne('/api/v1/edge/routes').flush('route refused', { status: 422, statusText: 'Unprocessable' });
      fixture.detectChanges();
      expect(text()).toContain('GridCraft is installed but has no address yet.');
      expect(text()).toContain('route refused');
      button('Try again')!.click();
      doneReads([]);
      http.expectOne('/api/v1/edge/routes').flush({ id: 32 });
    });

    it('offers the app when its edge route exists', () => {
      start(CRAFT, { installed: ['gridcraft'] });
      doneReads([{ Host: 'gridcraft.lan', Target: { Host: 'gridcraft', Port: 8080 }, Exposure: 'local', TLS: 'internal', CraftApp: 'gridcraft' }]);
      expect(c().current()).toBe('done');
      const open = (fixture.nativeElement as HTMLElement).querySelector('a[href="https://gridcraft.lan"]');
      expect(open?.textContent).toContain('Open GridCraft');
    });

    it('says what happened on failure, links to the operation and retries through a fresh review', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] } });
      review();
      button('Install GridCraft')!.click();
      http.expectOne('/api/v1/catalog/gridcraft/install').flush({ id: 9 });
      streams.get(9)!.next(opState(9, 'failed', 'catalog: compose up gridcraft: network homestead-edge not found'));
      fixture.detectChanges();
      expect(c().phase()).toBe('failed');
      expect(text()).toContain('compose up gridcraft: network homestead-edge not found');
      expect(text()).toContain('GridCraft was not installed.');
      expect(text()).toContain('Nothing is left half-installed and your data was kept');
      expect(text()).toContain('That did not work.');
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/operations/9"]')).not.toBeNull();
      button('Try again')!.click();
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft'));
      fixture.detectChanges();
      expect(c().current()).toBe('review');
      expect(c().installOperation()).toBeNull();
    });

    it('explains a refused install request', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] } });
      review();
      button('Install GridCraft')!.click();
      http
        .expectOne('/api/v1/catalog/gridcraft/install')
        .flush('catalog: "gridcraft" is already installed; use configure', { status: 409, statusText: 'Conflict' });
      fixture.detectChanges();
      expect(c().current()).toBe('install');
      expect(text()).toContain('already installed');
      expect(text()).toContain('Manage the installed app instead');
    });

    it('picks an install back up after the owner left and came back', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] } });
      review();
      button('Install GridCraft')!.click();
      http.expectOne('/api/v1/catalog/gridcraft/install').flush({ id: 11 });
      fixture.detectChanges();
      fixture.destroy();
      http.verify();
      // Back on the wizard: same operation, still installing.
      fixture = TestBed.createComponent(InstallWizard);
      fixture.detectChanges();
      http.expectOne('/api/v1/catalog/gridcraft').flush(CRAFT);
      http.expectOne('/api/v1/catalog').flush(PACKAGES());
      http.expectOne('/api/v1/docker/health').flush(HEALTH_WITH_CADDY);
      http.expectOne('/api/v1/craft').flush({ images: [], verdicts: [] });
      fixture.detectChanges();
      expect(c().current()).toBe('install');
      expect(c().installOperation()).toBe(11);
      expect(c().phase()).toBe('installing');
    });
  });

  describe('craft verify and build', () => {
    function reviewUnbuilt() {
      start(CRAFT);
      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft'));
      fixture.detectChanges();
    }

    it('asks for Verify and build before install when the image is not built', () => {
      reviewUnbuilt();
      expect(button('Install GridCraft')).toBeUndefined();
      expect(button('Verify and build GridCraft')).toBeDefined();
      expect(text()).toContain('built on this appliance from a signed download, never pulled');
    });

    it('builds, then offers install, and does not skip the verification', () => {
      reviewUnbuilt();
      button('Verify and build GridCraft')!.click();
      const build = http.expectOne('/api/v1/craft/build');
      expect(build.request.body).toEqual({ app: 'gridcraft' });
      build.flush({ id: 5 });
      fixture.detectChanges();
      expect(c().current()).toBe('install');
      expect(c().phase()).toBe('building');
      expect(text()).toContain('Verifying and building GridCraft');
      expect(TestBed.inject(WorkOrders).orders()[0].title).toBe('Build craft app');
      streams.get(5)!.next(opState(5, 'succeeded'));
      http.expectOne('/api/v1/craft').flush({ images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] });
      fixture.detectChanges();
      expect(c().phase()).toBe('built');
      expect(text()).toContain('GridCraft is built');
      expect(button('Install GridCraft')).toBeDefined();
      http.expectNone('/api/v1/catalog/gridcraft/install');
    });

    it('shows a refused build with the reason and offers another try', () => {
      reviewUnbuilt();
      button('Verify and build GridCraft')!.click();
      http.expectOne('/api/v1/craft/build').flush({ id: 6 });
      streams.get(6)!.next(opState(6, 'failed', 'craft: signature check failed'));
      http.expectOne('/api/v1/craft').flush({
        images: [],
        verdicts: [{ app: 'gridcraft', version: '0.7.0', counter: 1, state: 'failed', refusal: 'signature', detail: 'signature does not match a trusted key', key_id: '', override: false }],
      });
      fixture.detectChanges();
      expect(c().phase()).toBe('failed');
      expect(text()).toContain('signature check failed');
      expect(text()).toContain('craft signing keys on the Updates page');
      expect(button('Try again')).toBeDefined();
    });
  });

  it('assigns a chosen mount to a class that has none, before installing, and leaves existing assignments alone', () => {
    start(NEXTCLOUD, { assignments: [{ Class: 'bulk', Mountpoint: '/mnt/big', Actor: 'o', CreatedAt: '' }] });
    c().setMount('application', '/mnt/big');
    c().setMount('bulk', '/mnt/big');
    to('review');
    http.expectOne('/api/v1/catalog/nextcloud/plan').flush(plan('nextcloud'));
    fixture.detectChanges();
    button('Install Nextcloud')!.click();
    const assign = http.expectOne('/api/v1/storage/assignments');
    expect(assign.request.method).toBe('POST');
    expect(assign.request.body).toEqual({ class: 'application', mountpoint: '/mnt/big', actor: 'owner' });
    assign.flush({ Class: 'application', Mountpoint: '/mnt/big', Actor: 'owner', CreatedAt: '' });
    http.expectOne('/api/v1/catalog/nextcloud/install').flush({ id: 50 });
  });

  it('opens straight on the installed summary for an installed package', () => {
    start(NEXTCLOUD, { installed: ['nextcloud'] });
    doneReads([]);
    expect(c().current()).toBe('done');
    expect(text()).toContain('Nextcloud installed');
    expect(text()).toContain('create the administrator account');
    expect(text()).toContain('Uninstall Nextcloud');
  });

  it('inside setup, hands back with the given label, offers no uninstall and lists a shared folder once', () => {
    const shared = {
      ...NEXTCLOUD,
      Services: [
        NEXTCLOUD.Services[0],
        { Name: 'worker', Image: 'nextcloud:35.0.1-apache', Data: [{ Target: '/var/www/html', Class: 'application', Subpath: 'html' }] },
      ],
    };
    start(shared, {
      installed: ['nextcloud'],
      assignments: [{ Class: 'application', Mountpoint: '/mnt/big', Actor: 'o', CreatedAt: '' }],
      inputs: { embedded: true, appName: 'nextcloud', closeLabel: 'Continue to sign-in' },
    });
    doneReads([]);
    expect(c().current()).toBe('done');
    expect(button('Continue to sign-in')).toBeDefined();
    expect(button('Back to the app list')).toBeUndefined();
    expect(text()).not.toContain('Uninstall Nextcloud');
    const paths = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('.wizard-paths code')).map(
      (code) => code.textContent,
    );
    expect(paths.filter((path) => path === '/mnt/big/nextcloud/html').length).toBe(1);
  });

  it('says so when the package is not in the catalog', () => {
    TestBed.configureTestingModule({
      providers: [
        provideRouter([]),
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: ActivatedRoute, useValue: { paramMap: of(convertToParamMap({ name: 'nope' })) } },
      ],
    });
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(InstallWizard);
    fixture.detectChanges();
    http.expectOne('/api/v1/catalog/nope').flush('unknown package', { status: 404, statusText: 'Not Found' });
    // The list request is cancelled with the failed one.
    expect(http.match('/api/v1/catalog')).toHaveLength(1);
    fixture.detectChanges();
    expect(text()).toContain('This package is not in the catalog.');
  });
});
