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
    access: { mode: 'open', people: [], admin_supported: false },
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
      /** S313: the resolver (default on) and the app's public status (default no tunnel). */
      resolver?: boolean;
      publicStatus?: Record<string, unknown>;
      /** S315: the edge route table a path app reads its appliance names from. */
      edgeRoutes?: unknown[];
      /** Leave S390 access planning on HTTP for request/response assertions. */
      accessApi?: boolean;
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
    if (!options.accessApi) {
      vi.spyOn(TestBed.inject(ApiService), 'planCatalogAccess').mockReturnValue(of({ digest: 'access-digest' }));
    }
    http = TestBed.inject(HttpTestingController);
    fixture = TestBed.createComponent(InstallWizard);
    for (const [key, value] of Object.entries(options.inputs ?? {})) {
      fixture.componentRef.setInput(key, value);
    }
    fixture.detectChanges();
    http.expectOne(`/api/v1/catalog/${name}`).flush(manifest);
    http.expectOne('/api/v1/catalog').flush(PACKAGES(options.installed));
    for (const request of http.match('/api/v1/names')) {
      request.flush({ resolver: { enabled: options.resolver ?? true, running: true, listening: [], names: [], router_hint: '' }, apps: [] });
    }
    if (manifest['Serve'] === 'path') {
      http.expectOne('/api/v1/edge/routes').flush({ routes: options.edgeRoutes ?? [] });
    }
    for (const request of http.match(`/api/v1/catalog/${name}/public`)) {
      request.flush(options.publicStatus ?? { app: name, available: false, reason: 'No tunnel is set up yet.', routes: [], sign_in: false });
    }
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

  /** S324: the app's setup, as the appliance reports it to the setup panel. */
  function setupRead(view: Record<string, unknown>) {
    http.expectOne('/api/v1/catalog/gridcraft/setup').flush({ app: 'gridcraft', steps: [], ...view });
    fixture.detectChanges();
  }

  const GRIDCRAFT_ROUTE = { Host: 'gridcraft.lan', Target: { Host: 'gridcraft', Port: 8080 }, Exposure: 'local', TLS: 'internal', CraftApp: 'gridcraft' };

  beforeEach(() => {
    // The Done step asks this device to open the address; no network in specs.
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(''))));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    // The Done step's setup panel (S324) reads the app's setup; most specs have none.
    for (const request of http.match((req) => req.url.endsWith('/setup'))) {
      request.flush({ app: 'x', status: 'none', steps: [] });
    }
    // The advisory reach check (S313) runs after every finished Done step.
    for (const request of http.match((req) => req.url.endsWith('/reach'))) {
      request.flush({ app: 'x', resolver: true, addresses: [] });
    }
    // The review reads the people it can grant (S317); most specs do not care.
    for (const request of http.match('/api/v1/people')) {
      request.flush({ token_kept: false, people: [], unmanaged: [], apps: [], owner: 'owner', token_words: '' });
    }
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
      expect((fixture.nativeElement as HTMLElement).querySelector('app-operation-progress a[href="/admin/operations/21"]')).not.toBeNull();
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

    it('uses the exposed service container port without requiring a host port', () => {
      start({ ...NEXTCLOUD_NO_PORTS, Services: NEXTCLOUD.Services.map((service) => ({ ...service, ContainerPort: 8088 })) });
      expect(c().containerPorts()['nextcloud']).toBe('8088');
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
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/admin/storage"]')).not.toBeNull();
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
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/admin/exposure"]')).not.toBeNull();
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
      // S305: the review can connect sign-in during the install (S298).
      expect(text()).toContain('the review offers Sign in with Authentik as part of the install');
      expect(text()).toContain('The owner always keeps full access.');
      expect(text()).not.toContain('Installing does not change that');
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
      expect(install.request.body.setup).toEqual({});
      install.flush({ id: 7 });
      fixture.detectChanges();
      expect(c().current()).toBe('install');
      const rail = TestBed.inject(WorkOrders);
      expect(rail.orders().map((o) => o.title)).toEqual(['Install GridCraft']);
      expect(rail.orders()[0].done).toBe('GridCraft installed');
      expect((fixture.nativeElement as HTMLElement).querySelector('app-operation-progress a[href="/admin/operations/7"]')).not.toBeNull();

      streams.get(7)!.next(opState(7, 'running'));
      fixture.detectChanges();
      expect(c().current()).toBe('install');
      streams.get(7)!.next(opState(7, 'succeeded'));
      const other = { Host: 'notes.lan', Target: { Host: 'notes', Port: 3000 }, Exposure: 'local', TLS: 'internal', Health: { Path: '', IntervalSeconds: 0 } };
      doneReads([other]);
      expect(c().current()).toBe('done');
      // The appliance adds the address as the first step of the setup (S324); the page only follows it.
      expect(text()).toContain('The appliance gives GridCraft its address as the first step of its setup');
      setupRead({ status: 'running', install_operation: 7, operation: 60, steps: [{ name: 'address', state: 'running', digest: 'ad1' }] });
      expect(text()).toContain('Setting up GridCraft');
      expect(text()).toContain('Address on your network Running');
      streams.get(60)!.next(opState(60, 'succeeded'));
      setupRead({ status: 'done', install_operation: 7, operation: 60, steps: [{ name: 'address', state: 'done', digest: 'ad1' }] });
      expect(text()).toContain('GridCraft is set up');
      doneReads([other, GRIDCRAFT_ROUTE]);
      expect(text()).toContain('GridCraft installed');
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="https://gridcraft.lan"]')?.textContent).toContain('Open GridCraft');
      expect(text()).toContain('https://gridcraft.lan');
      expect(text()).toContain('There is no account to create.');
      expect(text()).toContain('GridCraft keeps nothing on the appliance');
      expect(c().reach()).toBe('checking');
    });

    it('requires explicit choices, pins disabled people to none, and uses the admin install route', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] }, accessApi: true });
      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { access: {
        mode: 'managed', admin_supported: true, people: [
          { id: 4, display_name: 'Sam Lee', username: 'sam', state: 'active' },
          { id: 6, display_name: 'Alex Chen', username: 'alex', state: 'active' },
          { id: 5, display_name: 'Pat Kim', username: 'pat', state: 'disabled' },
        ],
      }, sign_in: { offered: true, token_kept: true, plan: {
        package: 'gridcraft', title: 'GridCraft', enabled: true, adapter: 'craft', provider: 'Homestead: GridCraft',
        application: 'GridCraft', client_id: 'gridcraft', issuer: 'https://authentik.lan', redirect_uris: [],
        app_settings: [], changes: [], warnings: [], digest: 'si1',
      } } }));
      fixture.detectChanges();
      const el = fixture.nativeElement as HTMLElement;
      expect(text()).toContain('Choose a role for every managed person');
      expect(text()).toContain('Sam Lee (sam)');
      const signIn = el.querySelector('input[name="sign-in-wanted"]') as HTMLInputElement;
      expect(signIn.checked).toBe(true);
      expect(signIn.disabled).toBe(true);
      expect(button('Install GridCraft')!.disabled).toBe(true);
      const sam = el.querySelector('select[name="access-4"]') as HTMLSelectElement;
      sam.value = 'admin';
      sam.dispatchEvent(new Event('change'));
      fixture.detectChanges();
      expect(button('Install GridCraft')!.disabled).toBe(true);
      const alex = el.querySelector('select[name="access-6"]') as HTMLSelectElement;
      alex.value = 'none';
      alex.dispatchEvent(new Event('change'));
      fixture.detectChanges();
      const disabled = el.querySelector('select[name="access-5"]') as HTMLSelectElement;
      expect(disabled.disabled).toBe(true);
      expect(disabled.value).toBe('none');
      expect(button('Install GridCraft')!.disabled).toBe(false);
      button('Install GridCraft')!.click();
      const access = http.expectOne('/api/v1/catalog/gridcraft/access-plan');
      expect(access.request.body.choices).toEqual([
        { person_id: 4, role: 'admin' }, { person_id: 6, role: 'none' }, { person_id: 5, role: 'none' },
      ]);
      expect(access.request.body.domain).toBe('lan');
      access.flush({ digest: 'access-review' });
      const install = http.expectOne('/api/v1/catalog/gridcraft/install-admin');
      expect(install.request.body.setup.access).toEqual({ digest: 'access-review', choices: [
        { person_id: 4, role: 'admin' }, { person_id: 6, role: 'none' }, { person_id: 5, role: 'none' },
      ] });
      expect(install.request.body.setup.sign_in).toEqual({ digest: 'si1' });
      install.flush({ id: 7 });
      fixture.detectChanges();
    });

    it.each(['open', 'bootstrap', 'self-managed'] as const)('installs %s apps without requesting access decisions', (mode) => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] }, accessApi: true });
      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', {
        access: { mode, people: [], admin_supported: false },
      }));
      fixture.detectChanges();

      button('Install GridCraft')!.click();
      http.expectNone('/api/v1/catalog/gridcraft/access-plan');
      const install = http.expectOne('/api/v1/catalog/gridcraft/install');
      expect(install.request.body.setup).not.toHaveProperty('access');
      install.flush({ id: 8 });
    });

    it('shows the self-managed notice and renders no per-person selectors', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] }, accessApi: true });
      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { access: {
        mode: 'self-managed', people: [], admin_supported: false,
        notice: 'GridCraft keeps its own accounts. Homestead does not control who can sign in to it; create accounts in the app itself.',
      } }));
      fixture.detectChanges();
      expect(text()).toContain('keeps its own accounts');
      expect((fixture.nativeElement as HTMLElement).querySelector('select[name^="access-"]')).toBeNull();
      expect(button('Install GridCraft')!.disabled).toBe(false);
    });

    it('blocks installation when the app access integration is unavailable', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] } });
      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { access: {
        mode: 'unavailable', admin_supported: false, people: [], reason: 'No enforceable access integration is ready.',
      } }));
      fixture.detectChanges();
      expect(text()).toContain('Access cannot be enforced for this app.');
      expect(button('Install GridCraft')!.disabled).toBe(true);
    });

    it('clears prior choices when the daemon returns a changed person roster', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] }, accessApi: true });
      const signIn = { offered: true, token_kept: true, plan: {
        package: 'gridcraft', title: 'GridCraft', enabled: true, adapter: 'craft', provider: 'Homestead: GridCraft',
        application: 'GridCraft', client_id: 'gridcraft', issuer: 'https://authentik.lan', redirect_uris: [],
        app_settings: [], changes: [], warnings: [], digest: 'si1',
      } };
      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { access: {
        mode: 'managed', admin_supported: false, people: [
          { id: 4, display_name: 'Sam Lee', username: 'sam', state: 'active' },
        ],
      }, sign_in: signIn }));
      fixture.detectChanges();
      const sam = (fixture.nativeElement as HTMLElement).querySelector('select[name="access-4"]') as HTMLSelectElement;
      sam.value = 'user';
      sam.dispatchEvent(new Event('change'));
      fixture.detectChanges();
      expect(button('Install GridCraft')!.disabled).toBe(false);

      c().replan().subscribe();
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { access: {
        mode: 'managed', admin_supported: false, people: [
          { id: 4, display_name: 'Sam Lee', username: 'sam', state: 'active' },
          { id: 7, display_name: 'Rae Park', username: 'rae', state: 'active' },
        ],
      }, sign_in: signIn }));
      fixture.detectChanges();
      expect(((fixture.nativeElement as HTMLElement).querySelector('select[name="access-4"]') as HTMLSelectElement).value).toBe('');
      expect(button('Install GridCraft')!.disabled).toBe(true);
    });

    it('lists the Nextcloud link in the review and approves it with the install (S326)', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] } });
      to('review');
      const filesLink = {
        apps: ['gridcraft'], lines: ['Link GridCraft to Nextcloud for files'], base_url: 'https://homestead.lan/apps/nextcloud',
        account: 'admin', root: 'Homestead', new_link: true, digest: 'fl1',
      };
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { files_link: filesLink }));
      fixture.detectChanges();
      const text = (fixture.nativeElement as HTMLElement).textContent ?? '';
      expect(text).toContain('Link GridCraft to Nextcloud for files');
      expect(text).toContain('as account admin with an app password Homestead creates');
      button('Install GridCraft')!.click();
      const install = http.expectOne('/api/v1/catalog/gridcraft/install');
      expect(install.request.body.setup).toEqual({ files_link: { digest: 'fl1' } });
      install.flush({ id: 7 });
    });

    it('approves sign-in with the install and shows where its setup stopped (S298, S324)', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] } });
      to('review');
      const preview = {
        offered: true,
        token_kept: true,
        plan: {
          package: 'gridcraft', title: 'GridCraft', enabled: true, adapter: 'immich', provider: 'Homestead: GridCraft', application: 'GridCraft',
          client_id: 'homestead-gridcraft', issuer: 'x', redirect_uris: [], app_settings: [], changes: [], warnings: [], digest: 'sd2',
        },
      };
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { sign_in: preview }));
      fixture.detectChanges();
      expect((fixture.nativeElement as HTMLElement).querySelector('input[name="sign-in-token"]')).toBeNull();
      button('Install GridCraft')!.click();
      const install = http.expectOne('/api/v1/catalog/gridcraft/install');
      expect(install.request.body.setup).toEqual({ sign_in: { digest: 'sd2' } });
      install.flush({ id: 7 });
      streams.get(7)!.next(opState(7, 'succeeded'));
      doneReads([]);
      setupRead({
        status: 'stopped', install_operation: 7, operation: 61,
        reason: 'Stopped at the sign-in with Authentik: the plan changed after it was approved',
        steps: [
          { name: 'address', state: 'done', digest: 'ad1' },
          { name: 'sign-in', state: 'changed', digest: 'sd2', reason: 'the plan changed after it was approved' },
        ],
      });
      doneReads([GRIDCRAFT_ROUTE]);
      expect(text()).toContain("GridCraft's setup stopped");
      expect(text()).toContain('Sign-in with Authentik Plan changed');
      expect(text()).toContain('Stopped at the sign-in with Authentik');
      expect(button('Review')).toBeDefined();
      expect(button('Skip the rest')).toBeDefined();
      http.expectNone('/api/v1/catalog/gridcraft/sign-in/apply');
    });

    it('says the app is not reachable and links to Trust when this device cannot open the address', async () => {
      vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('cert'))));
      start(CRAFT, { installed: ['gridcraft'] });
      doneReads([{ Host: 'gridcraft.lan', Target: { Host: 'gridcraft', Port: 8080 }, Exposure: 'local', TLS: 'internal', CraftApp: 'gridcraft' }]);
      await Promise.resolve();
      await Promise.resolve();
      fixture.detectChanges();
      expect(text()).toContain('could not open that address securely');
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/admin/trust"]')).not.toBeNull();
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
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/admin/edge"]')).not.toBeNull();
    });

    it('says plainly when the setup stopped before the address and offers to review it', () => {
      start(CRAFT, { craft: { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] } });
      review();
      button('Install GridCraft')!.click();
      http.expectOne('/api/v1/catalog/gridcraft/install').flush({ id: 7 });
      streams.get(7)!.next(opState(7, 'succeeded'));
      doneReads([]);
      setupRead({
        status: 'stopped', install_operation: 7, operation: 60,
        reason: 'Stopped at the address on your network: route refused',
        steps: [{ name: 'address', state: 'failed', digest: 'ad1', reason: 'route refused' }],
      });
      doneReads([]);
      expect(text()).toContain('GridCraft has no address yet because its setup stopped first.');
      expect(text()).toContain('route refused');
      expect(button('Review')).toBeDefined();
      // The page adds no route itself: the setup owns that step.
      http.expectNone((req) => req.method === 'POST' && req.url === '/api/v1/edge/routes');
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
      expect((fixture.nativeElement as HTMLElement).querySelector('a[href="/admin/operations/9"]')).not.toBeNull();
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
      http.expectOne('/api/v1/names').flush({ resolver: { enabled: true }, apps: [] });
      http.expectOne('/api/v1/catalog/gridcraft/public').flush({ app: 'gridcraft', available: false, routes: [], sign_in: false });
      http.expectOne('/api/v1/docker/health').flush(HEALTH_WITH_CADDY);
      http.expectOne('/api/v1/craft').flush({ images: [], verdicts: [] });
      fixture.detectChanges();
      expect(c().current()).toBe('install');
      expect(c().installOperation()).toBe(11);
      expect(c().phase()).toBe('installing');
    });
  });

  describe('the local and public address (S313)', () => {
    const BUILT = { images: [{ app: 'gridcraft', version: '0.7.0', present: true, match: true }], verdicts: [] };
    const TUNNEL = {
      app: 'gridcraft', available: false, reason: 'Install GridCraft first.', tunnel_id: 'tid', tunnel: 'homestead',
      routes: [], proposed: 'gridcraft.homestead.example.com', sign_in: false,
    };
    const SIGN_IN = {
      offered: true,
      token_kept: true,
      plan: {
        package: 'gridcraft', title: 'GridCraft', enabled: true, adapter: 'immich', provider: 'Homestead: GridCraft', application: 'GridCraft',
        client_id: 'homestead-gridcraft', issuer: 'x', redirect_uris: ['https://gridcraft.homestead.example.com/auth/login'], app_settings: [],
        changes: [], warnings: [], digest: 'sd3',
      },
    };
    function publicPlan(digest: string, hostname = 'gridcraft.homestead.example.com') {
      return {
        app: 'gridcraft', action: 'add', hostname, route: 'gridcraft.lan', tunnel: 'homestead', tunnel_id: 'tid', zone: 'example.com',
        dns: [{ name: hostname, type: 'CNAME', content: 'tid.cfargotunnel.com', proxied: true, action: 'create', replaces: [] }],
        connector_before: [], connector_after: [hostname],
        coverage: [{ hostname, covered: false, checked: true, by: '' }],
        warnings: ['Browsers will fail to connect until Cloudflare has a certificate for ' + hostname + '.'],
        app_settings: [], digest,
      };
    }
    const el = () => fixture.nativeElement as HTMLElement;

    it('shows both local names, and says .lan needs the resolver when it is off', () => {
      start(CRAFT, { resolver: false });
      to('address');
      expect(text()).toContain('https://gridcraft.lan');
      expect(text()).toContain('https://gridcraft.local');
      expect(el().querySelector('[data-resolver-off]')).not.toBeNull();
      expect(el().querySelector('a[href="/admin/names"]')).not.toBeNull();
      expect(text()).toContain('Until then, use https://gridcraft.local');
    });

    it('shows a host app its own name, not a path under the appliance (S315)', () => {
      start(CRAFT);
      to('address');
      expect(el().querySelector('[data-path-app]')).toBeNull();
      expect(text()).not.toContain('/apps/gridcraft/');
    });

    describe('an app served by path (S315, ADR-044)', () => {
      const PATH_APP = { ...NEXTCLOUD, Serve: 'path', Path: '/apps/nextcloud/' };
      const DASH = [{ Host: 'homestead.lan', Aliases: ['homestead.local'], Dashboard: true, Upstream: 'http://127.0.0.1:8080' }];
      const PATH_TUNNEL = {
        app: 'nextcloud', available: false, reason: 'Install Nextcloud first.', tunnel_id: 'tid', tunnel: 'homestead',
        routes: [], proposed: 'homestead.example.com', proposed_url: 'https://homestead.example.com/apps/nextcloud/', sign_in: false,
      };

      it('shows its path under each appliance name and asks for no name', () => {
        start(PATH_APP, { edgeRoutes: DASH });
        to('address');
        expect(el().querySelector('[data-path-app]')).not.toBeNull();
        const local = el().querySelector('ul[aria-label="Local addresses"]')?.textContent ?? '';
        expect(local).toContain('https://homestead.lan/apps/nextcloud/');
        expect(local).toContain('https://homestead.local/apps/nextcloud/');
        expect(text()).not.toContain('https://nextcloud.lan');
        expect(el().querySelector('[data-resolver-off]')).toBeNull();
      });

      it('says homestead.lan needs the resolver when it is off', () => {
        start(PATH_APP, { edgeRoutes: DASH, resolver: false });
        to('address');
        const note = el().querySelector('[data-resolver-off]')?.textContent ?? '';
        expect(note).toContain('homestead.lan');
        expect(note).toContain('Until then, use https://homestead.local/apps/nextcloud/');
      });

      it('offers publishing at its path under the dashboard\'s public name, with no name to type', () => {
        start(PATH_APP, { edgeRoutes: DASH, publicStatus: PATH_TUNNEL });
        to('address');
        expect(el().querySelector('[data-public-url]')?.textContent).toBe('https://homestead.example.com/apps/nextcloud/');
        expect(el().querySelector('input[name="public-name"]')).toBeNull();
        expect(el().querySelector('input[name="publish"]')).not.toBeNull();
      });

      it('says to publish the dashboard first when it has no public name', () => {
        start(PATH_APP, { edgeRoutes: DASH, publicStatus: { ...PATH_TUNNEL, proposed: '', proposed_url: '' } });
        to('address');
        expect(el().querySelector('[data-path-needs-server]')?.textContent).toContain('Publish the dashboard');
        expect(el().querySelector('input[name="publish"]')).toBeNull();
      });
    });

    it('says nothing about the resolver when it is on', () => {
      start(CRAFT, { resolver: true });
      to('address');
      expect(text()).toContain('https://gridcraft.local');
      expect(el().querySelector('[data-resolver-off]')).toBeNull();
    });

    it('says there is no tunnel and links to the Tunnel page, with no publish toggle', () => {
      start(CRAFT);
      to('address');
      expect(el().querySelector('[data-no-tunnel]')).not.toBeNull();
      expect(el().querySelector('a[href="/admin/tunnel"]')).not.toBeNull();
      expect(el().querySelector('input[name="publish"]')).toBeNull();
      expect(c().publish()).toBe(false);
    });

    it('starts publishing off, with the warning, when the install sets up no sign-in', () => {
      start(CRAFT, { publicStatus: TUNNEL });
      to('address');
      const toggle = el().querySelector('input[name="publish"]') as HTMLInputElement;
      expect(toggle.checked).toBe(false);
      expect(el().querySelector('[data-reach-public]')).toBeNull();
      expect((el().querySelector('input[name="public-name"]') as HTMLInputElement).value).toBe('gridcraft.homestead.example.com');
      expect(text()).toContain('only its own sign-in would protect it');
      // Turning it on plans the name before the install and shows its certificate lamp.
      toggle.checked = true;
      toggle.dispatchEvent(new Event('change'));
      const planned = http.expectOne('/api/v1/catalog/gridcraft/public/plan');
      expect(planned.request.body).toEqual({ enabled: true, hostname: 'gridcraft.homestead.example.com', route: 'gridcraft.lan', before_install: true });
      planned.flush(publicPlan('pd1'));
      fixture.detectChanges();
      expect(text()).toContain('no certificate yet');
      expect(text()).toContain('Its own sign-in is what protects it.');
    });

    it('starts publishing on when the same install sets up sign-in with Authentik', () => {
      start(CRAFT, { publicStatus: TUNNEL, craft: BUILT });
      to('preflight');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { sign_in: SIGN_IN, digest: 'id1' }));
      to('address');
      http.expectOne('/api/v1/catalog/gridcraft/public/plan').flush(publicPlan('pd1'));
      fixture.detectChanges();
      expect((el().querySelector('input[name="publish"]') as HTMLInputElement).checked).toBe(true);
      expect(text()).toContain('sets up sign-in with Authentik');
      expect(el().querySelector('[data-reach-public]')?.textContent).toContain('Local network and the tunnel');
    });

    it('reviews managed publication for the state after sign-in and access setup', () => {
      start(CRAFT, { publicStatus: TUNNEL, craft: BUILT, accessApi: true });
      to('preflight');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', {
        access: { mode: 'managed', people: [], admin_supported: false },
        sign_in: SIGN_IN,
      }));
      to('address');
      fixture.detectChanges();
      expect(text()).toContain('Publication waits until Authentik sign-in and person access setup finish');
      expect(el().querySelector('input[name="publish"]')).not.toBeNull();
      const firstPublic = http.expectOne('/api/v1/catalog/gridcraft/public/plan');
      expect(firstPublic.request.body.anticipate_sign_in).toBe(true);
      firstPublic.flush(publicPlan('original-public'));

      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', {
        access: { mode: 'managed', people: [], admin_supported: false },
        sign_in: SIGN_IN,
      }));
      const secondPublic = http.expectOne('/api/v1/catalog/gridcraft/public/plan');
      expect(secondPublic.request.body.anticipate_sign_in).toBe(true);
      secondPublic.flush(publicPlan('original-public'));
      fixture.detectChanges();
      button('Approve both and install GridCraft')!.click();
      const access = http.expectOne('/api/v1/catalog/gridcraft/access-plan');
      expect(access.request.body.public_name).toBe('gridcraft.homestead.example.com');
      access.flush({ digest: 'access-reviewed' });
      const install = http.expectOne('/api/v1/catalog/gridcraft/install');
      expect(install.request.body.setup.public.digest).toBe('original-public');
      expect(install.request.body.setup.access).toEqual({ digest: 'access-reviewed', choices: [] });
      install.flush({ id: 44 });
    });

    function reviewBoth(digest = 'pd1') {
      start(CRAFT, { publicStatus: TUNNEL, craft: BUILT });
      to('preflight');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { sign_in: SIGN_IN, digest: 'id1' }));
      to('address');
      http.expectOne('/api/v1/catalog/gridcraft/public/plan').flush(publicPlan(digest));
      to('review');
      const installPlan = http.expectOne('/api/v1/catalog/gridcraft/plan');
      expect(installPlan.request.body.public_name).toBe('gridcraft.homestead.example.com');
      installPlan.flush(plan('gridcraft', { sign_in: SIGN_IN, digest: 'id1' }));
      http.expectOne('/api/v1/catalog/gridcraft/public/plan').flush(publicPlan(digest));
      fixture.detectChanges();
    }

    it('reviews two permits and approves both with one install', () => {
      reviewBoth();
      const permits = Array.from(el().querySelectorAll('app-permit'));
      expect(permits.length).toBe(2);
      expect(permits[0].querySelector('.permit-seal')?.getAttribute('aria-label')).toBe('Plan digest id1');
      expect(permits[1].querySelector('.permit-seal')?.getAttribute('aria-label')).toBe('Plan digest pd1');
      expect(permits[1].textContent).toContain('CNAME gridcraft.homestead.example.com');
      button('Approve both and install GridCraft')!.click();
      const install = http.expectOne('/api/v1/catalog/gridcraft/install');
      expect(install.request.body.digest).toBe('id1');
      expect(install.request.body.public_name).toBe('gridcraft.homestead.example.com');
      expect(install.request.body.setup.public).toEqual({ hostname: 'gridcraft.homestead.example.com', route: 'gridcraft.lan', digest: 'pd1' });
      expect(install.request.body.setup).not.toHaveProperty('access');
      install.flush({ id: 7 });
      fixture.detectChanges();
      // Both permits carry their Approved stamp while it installs.
      expect(el().querySelectorAll('.permit-stamp[data-kind="approved"]').length).toBe(2);
    });

    it('after the install: the appliance runs the address, the public address by its digest and sign-in, then the check', () => {
      reviewBoth();
      button('Approve both and install GridCraft')!.click();
      const install = http.expectOne('/api/v1/catalog/gridcraft/install');
      expect(install.request.body.setup).toEqual({
        public: { hostname: 'gridcraft.homestead.example.com', route: 'gridcraft.lan', digest: 'pd1' },
        sign_in: { digest: 'sd3' },
      });
      install.flush({ id: 7 });
      streams.get(7)!.next(opState(7, 'succeeded'));
      doneReads([]);
      setupRead({
        status: 'running', install_operation: 7, operation: 60,
        steps: [
          { name: 'address', state: 'done', digest: 'ad1' },
          { name: 'publish', state: 'running', digest: 'pd1' },
          { name: 'sign-in', state: 'waiting', digest: 'sd3' },
        ],
      });
      expect(text()).toContain('Public address Running');
      expect(text()).toContain('Sign-in with Authentik Waiting');
      // Nothing is applied from this page.
      http.expectNone('/api/v1/catalog/gridcraft/public/apply');
      http.expectNone('/api/v1/catalog/gridcraft/sign-in/apply');
      streams.get(60)!.next(opState(60, 'succeeded'));
      setupRead({
        status: 'done', install_operation: 7, operation: 60,
        steps: [
          { name: 'address', state: 'done', digest: 'ad1' },
          { name: 'publish', state: 'done', digest: 'pd1' },
          { name: 'sign-in', state: 'done', digest: 'sd3' },
        ],
      });
      doneReads([GRIDCRAFT_ROUTE]);
      http.expectOne('/api/v1/catalog/gridcraft/reach').flush({
        app: 'gridcraft', resolver: false,
        addresses: [
          { address: 'https://gridcraft.lan', host: 'gridcraft.lan', kind: 'local', answers: true,
            reason: 'Your devices cannot resolve gridcraft.lan while the appliance resolver is off.',
            fix: 'Turn on the resolver on the Names page, or add a router DNS entry for gridcraft.lan.' },
          { address: 'https://gridcraft.homestead.example.com', host: 'gridcraft.homestead.example.com', kind: 'public', answers: false,
            reason: 'Browsers will fail to connect until Cloudflare has a certificate for gridcraft.homestead.example.com.' },
        ],
      });
      fixture.detectChanges();
      expect(text()).toContain('GridCraft is set up');
      expect(text()).toContain('Public address Done');
      // The approved public plan keeps its stamp once the setup published it.
      expect(Array.from(el().querySelectorAll('.permit-seal')).some((seal) => seal.getAttribute('aria-label') === 'Plan digest pd1')).toBe(true);
      const rows = Array.from(el().querySelectorAll('.reach-list li'));
      expect(rows.map((row) => row.querySelector('.badge')!.textContent!.trim())).toEqual(['answers', 'does not answer yet']);
      expect(rows[0].querySelector('.badge')!.classList).toContain('badge-ok');
      expect(rows[1].querySelector('.badge')!.classList).toContain('badge-warn');
      expect(text()).toContain('add a router DNS entry for gridcraft.lan');
      expect(text()).toContain('certificate for gridcraft.homestead.example.com');
    });

    it('shows a changed public plan to approve again, and resumes the setup with its new digest', () => {
      reviewBoth();
      button('Approve both and install GridCraft')!.click();
      http.expectOne('/api/v1/catalog/gridcraft/install').flush({ id: 7 });
      streams.get(7)!.next(opState(7, 'succeeded'));
      doneReads([]);
      const stopped = {
        status: 'stopped', install_operation: 7, operation: 60,
        reason: 'Stopped at the public address: the plan changed after it was approved',
        steps: [
          { name: 'address', state: 'done', digest: 'ad1' },
          { name: 'publish', state: 'changed', digest: 'pd1', reason: 'the plan changed after it was approved' },
          { name: 'sign-in', state: 'waiting', digest: 'sd3' },
        ],
      };
      setupRead(stopped);
      doneReads([GRIDCRAFT_ROUTE]);
      expect(text()).toContain('Public address Plan changed');
      button('Review')!.click();
      setupRead({ ...stopped, review: { step: 'publish', digest: 'pd2', public: publicPlan('pd2') } });
      expect(el().querySelector('app-app-setup .permit-seal')?.getAttribute('aria-label')).toBe('Plan digest pd2');
      expect(text()).toContain('Publishes GridCraft at gridcraft.homestead.example.com');
      // Nothing else runs until the owner answers.
      http.expectNone('/api/v1/catalog/gridcraft/setup/resume');
      button('Approve and resume')!.click();
      const resume = http.expectOne('/api/v1/catalog/gridcraft/setup/resume');
      expect(resume.request.body).toEqual({ digest: 'pd2' });
      resume.flush({ id: 62 });
      streams.get(62)!.next(opState(62, 'succeeded'));
      setupRead({
        status: 'done', install_operation: 7, operation: 62,
        steps: [
          { name: 'address', state: 'done', digest: 'ad1' },
          { name: 'publish', state: 'done', digest: 'pd2' },
          { name: 'sign-in', state: 'done', digest: 'sd3' },
        ],
      });
      expect(text()).toContain('GridCraft is set up');
    });

    it('sends the install digest and refuses a stale install plan', () => {
      start(CRAFT, { craft: BUILT });
      to('review');
      http.expectOne('/api/v1/catalog/gridcraft/plan').flush(plan('gridcraft', { digest: 'id9' }));
      fixture.detectChanges();
      expect(el().querySelectorAll('app-permit').length).toBe(1);
      button('Install GridCraft')!.click();
      const install = http.expectOne('/api/v1/catalog/gridcraft/install');
      expect(install.request.body.digest).toBe('id9');
      expect(install.request.body.public_name).toBeUndefined();
      install.flush('the install plan changed since it was reviewed; review it again', { status: 409, statusText: 'Conflict' });
      fixture.detectChanges();
      expect(text()).toContain('The install plan changed after you reviewed it.');
      expect(text()).toContain('Nothing was installed');
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
