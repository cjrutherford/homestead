import {
  Component,
  DestroyRef,
  ElementRef,
  Injector,
  afterNextRender,
  computed,
  effect,
  inject,
  input,
  OnInit,
  output,
  signal,
  viewChild,
} from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { forkJoin, Observable, of } from 'rxjs';
import { catchError, map, switchMap } from 'rxjs/operators';
import {
  ApiService,
  CatalogManifest,
  DockerHealth,
  EdgeRoute,
  InstallPlan,
  OperationState,
  StorageMount,
} from '../core/api.service';
import { registerPackageTitles } from '../core/operation-labels';
import { isTerminal } from '../core/operation-view';
import { ToastService } from '../core/toast.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';
import { appRoutes, mergeRoutes, routeFromManifest } from './app-routes';
import { BrowserAdvisories } from './browser-advisories';
import { curatedFor } from './curated-copy';
import { FolderRow, InstallDraft, InstallDrafts } from './install-drafts';
import { UninstallApp } from './uninstall-app';
import {
  EdgeReadiness,
  PlanProblem,
  StepDef,
  StepId,
  defaultContainerPort,
  defaultHostname,
  edgeReadiness,
  endpointsOf,
  explainPlanError,
  isCraft,
  memory,
  planSummary,
  size,
  ssoClass,
  stepsFor,
  storageClasses,
} from './wizard-model';

/** A registered mount the owner can put an app on. */
export interface MountOption {
  mountpoint: string;
  label: string;
  free: number;
  total: number;
}

/** Where the Install step is. */
export type InstallPhase = 'ready' | 'building' | 'built' | 'installing' | 'failed';

const CLASS_WORDS: Record<string, { name: string; help: string }> = {
  application: { name: 'App data', help: 'Settings, databases and other files the app needs to run.' },
  bulk: { name: 'Large files', help: 'Libraries such as media, photos and uploaded files.' },
};

const SECRET_WORDS: Record<string, string> = {
  db_password: 'the database password, so the app can talk to its database',
  secret_key: 'the secret key that protects sessions and stored data',
};

/** One sentence per step, drawn from what the manifest says. */
function helpFor(id: StepId, title: string): string {
  switch (id) {
    case 'about':
      return `Here is what ${title} is and what it does on your appliance.`;
    case 'preflight':
      return `Homestead checks that this appliance can run ${title} before anything changes.`;
    case 'storage':
      return `Choose where ${title} keeps its files; only storage you have already mounted and registered is listed. A kind of storage with no default yet gets your choice as its default.`;
    case 'address':
      return `Choose the name you type to reach ${title} on your network.`;
    case 'sign-in':
      return `These are the passwords Homestead creates for ${title} and how people sign in to it.`;
    case 'review':
      return 'This is exactly what will happen, and nothing has changed yet.';
    case 'install':
      return `Homestead is installing ${title}. You can leave this page and it keeps going in the work orders list.`;
    default:
      return 'Here is what to do next.';
  }
}

/**
 * The catalog install wizard (S285). One explicit sequence for every
 * package; a step that does not apply is left out (see `stepsFor`). It calls
 * the same routes the old install panel did, in the same order: plan, then
 * install. The plan on the Review step is asked for again every time the
 * step is entered, so the plan shown is the plan that is applied. Craft apps
 * add "Verify and build" before install, as the Updates page does.
 */
@Component({
  imports: [
    RouterLink,
    Loading,
    StateMessage,
    BrowserAdvisories,
    OperationProgress,
    UninstallApp,
    Dialog,
  ],
  selector: 'app-install-wizard',
  templateUrl: './install-wizard.html',
})
export class InstallWizard implements OnInit {
  private readonly api = inject(ApiService);
  private readonly route = inject(ActivatedRoute);
  /**
   * Inside the setup wizard's apps step (S291) the package comes from this
   * input instead of the route, the page heading is left to the step, and
   * the way out is `closed` instead of a link to the catalog.
   */
  readonly embedded = input(false);
  readonly appName = input('');
  readonly closed = output<void>();
  /** Opened from the setup walkthrough (S287): offer the way back. */
  protected readonly fromSetup = this.route.snapshot?.queryParamMap?.get('from') === 'setup';
  private readonly drafts = inject(InstallDrafts);
  private readonly toast = inject(ToastService);
  private readonly destroyRef = inject(DestroyRef);
  private readonly injector = inject(Injector);

  private readonly heading = viewChild<ElementRef<HTMLElement>>('stepHeading');

  protected readonly name = signal('');
  protected readonly loading = signal(true);
  protected readonly loadError = signal<string | null>(null);
  protected readonly manifest = signal<CatalogManifest | null>(null);
  protected readonly installed = signal(false);
  protected readonly stepIndex = signal(0);

  protected readonly domain = signal('lan');
  protected readonly hostnames = signal<Record<string, string>>({});
  protected readonly containerPorts = signal<Record<string, string>>({});
  protected readonly mountpoints = signal<Record<string, string>>({});
  protected readonly folders = signal<FolderRow[]>([]);
  protected readonly acknowledgedEdge = signal(false);
  protected readonly zone = signal('');
  protected readonly publicIp = signal('');
  protected readonly extraNetworks = signal('');

  protected readonly health = signal<DockerHealth | null>(null);
  protected readonly healthUnknown = signal(false);
  protected readonly mountOptions = signal<MountOption[]>([]);
  /** Classes that already have an assigned mount, so the install only fills gaps. */
  private readonly assignedClasses = signal<Record<string, string>>({});
  protected readonly storageError = signal<string | null>(null);
  protected readonly craftBuilt = signal<boolean | null>(null);
  protected readonly buildRefusal = signal<string | null>(null);
  protected readonly edgeRoute = signal<EdgeRoute | null>(null);
  /** The addresses the reviewed plan gives the app; added to the edge once it is installed. */
  protected readonly routesToAdd = signal<EdgeRoute[]>([]);
  /** Where the address stands on the Done step. */
  protected readonly routeState = signal<'checking' | 'adding' | 'added' | 'failed' | 'no-edge' | 'none'>('checking');
  protected readonly routeProblem = signal<string | null>(null);
  protected readonly routeOperation = signal<number | null>(null);
  protected readonly routeOpState = signal<OperationState | null>(null);
  /** Whether this device could open the address securely; null until tried. */
  protected readonly reach = signal<'checking' | 'ok' | 'failed' | null>(null);
  protected readonly edgeDialog = signal(false);
  protected readonly edgeSetup = signal<'idle' | 'starting' | 'running' | 'done' | 'failed'>('idle');
  protected readonly edgeOperation = signal<number | null>(null);
  protected readonly edgeOpState = signal<OperationState | null>(null);
  protected readonly edgeProblem = signal<string | null>(null);

  protected readonly plan = signal<InstallPlan | null>(null);
  protected readonly planning = signal(false);
  protected readonly planProblem = signal<PlanProblem | null>(null);
  /** The plan could not be asked for yet because storage is not chosen. */
  protected readonly planDeferred = signal(false);
  protected readonly stepProblem = signal<string | null>(null);
  /** The owner pressed Continue on this step, so field errors may show. */
  protected readonly attempted = signal(false);

  protected readonly phase = signal<InstallPhase>('ready');
  protected readonly buildOperation = signal<number | null>(null);
  protected readonly installOperation = signal<number | null>(null);
  protected readonly operation = signal<OperationState | null>(null);
  protected readonly failure = signal<PlanProblem | null>(null);
  protected readonly starting = signal(false);

  protected readonly steps = computed<StepDef[]>(() => {
    const manifest = this.manifest();
    return manifest ? stepsFor(manifest) : [];
  });
  protected readonly current = computed<StepId>(() => this.steps()[this.stepIndex()]?.id ?? 'about');
  protected readonly title = computed(() => this.manifest()?.Title ?? this.name());
  protected readonly craft = computed(() => {
    const manifest = this.manifest();
    return manifest !== null && isCraft(manifest);
  });
  protected readonly classes = computed(() => {
    const manifest = this.manifest();
    return manifest ? storageClasses(manifest) : [];
  });
  protected readonly endpoints = computed(() => {
    const manifest = this.manifest();
    return manifest ? endpointsOf(manifest) : [];
  });
  protected readonly routedServices = computed(() =>
    (this.manifest()?.Services ?? []).filter((service) => service.Exposes !== ''),
  );
  protected readonly edge = computed<EdgeReadiness | null>(() => {
    const health = this.health();
    return health ? edgeReadiness(health) : null;
  });
  protected readonly dockerDown = computed(() => this.edge()?.dockerReachable === false);
  protected readonly caddyMissing = computed(() => {
    const edge = this.edge();
    return edge !== null && edge.dockerReachable && !edge.caddyRunning;
  });
  protected readonly curated = computed(() => curatedFor(this.name()));
  protected readonly sso = computed(() => {
    const manifest = this.manifest();
    return manifest ? ssoClass(manifest) : 'unsupported';
  });
  protected readonly protocols = computed(() =>
    (this.manifest()?.SSO?.Protocols ?? []).map((protocol) => protocol.toUpperCase()).join(', '),
  );
  protected readonly secrets = computed(() =>
    (this.manifest()?.Secrets ?? []).map((secret) => ({
      name: secret.Name,
      immutable: secret.Immutable,
      purpose: SECRET_WORDS[secret.Name] ?? `a secret that ${this.title()} uses`,
    })),
  );
  /** The daemon's plan notes, less the ones that are no longer true on this appliance. */
  protected readonly planNotes = computed(() =>
    (this.plan()?.Notes ?? []).filter(
      (note) =>
        !(note.includes('does not deploy the edge proxy') && this.edge()?.caddyRunning) &&
        !(note.includes('craft build first') && this.craftBuilt() === true),
    ),
  );
  protected readonly summary = computed(() => {
    const plan = this.plan();
    return plan ? planSummary(plan) : null;
  });
  protected readonly rawPlan = computed(() => {
    const plan = this.plan();
    if (!plan) {
      return '';
    }
    return JSON.stringify(plan, null, 2);
  });
  protected readonly help = computed(() => {
    if (this.current() === 'install') {
      const title = this.title();
      switch (this.phase()) {
        case 'building':
          return `Homestead is verifying and building ${title}. You can leave this page and it keeps going in the work orders list.`;
        case 'built':
          return `${title} is built. The last step is to install it.`;
        case 'failed':
          return 'That did not work. Here is what happened and what to do next.';
        default:
          return helpFor('install', title);
      }
    }
    return helpFor(this.current(), this.title());
  });
  protected readonly headingText = computed(() => {
    const id = this.current();
    switch (id) {
      case 'about':
        return `What ${this.title()} is`;
      case 'done':
        return 'Next steps';
      default:
        return this.steps()[this.stepIndex()]?.label ?? '';
    }
  });
  /** Where each kind of storage will end up, as the owner will see it on disk. */
  protected readonly dataLocations = computed(() => {
    const manifest = this.manifest();
    if (manifest === null) {
      return [];
    }
    const mounts = this.mountpoints();
    const rows: { path: string; class: string }[] = [];
    for (const service of manifest.Services) {
      for (const data of service.Data ?? []) {
        const mount = mounts[data.Class];
        if (!data.Ephemeral && mount) {
          rows.push({ path: `${mount}/${manifest.Name}/${data.Subpath}`, class: data.Class });
        }
      }
    }
    return rows;
  });

  constructor() {
    effect(() => {
      const name = this.name();
      if (!name || this.loading() || this.manifest() === null) {
        return;
      }
      const draft: InstallDraft = {
        step: this.current(),
        domain: this.domain(),
        hostnames: this.hostnames(),
        containerPorts: this.containerPorts(),
        mountpoints: this.mountpoints(),
        folders: this.folders(),
        acknowledgedEdge: this.acknowledgedEdge(),
        zone: this.zone(),
        publicIp: this.publicIp(),
        networks: this.extraNetworks(),
        buildOperation: this.buildOperation(),
        installOperation: this.installOperation(),
        routes: this.routesToAdd(),
      };
      this.drafts.save(name, draft);
    });
  }

  ngOnInit(): void {
    if (this.embedded() && this.appName()) {
      this.name.set(this.appName());
      this.load(this.appName());
      return;
    }
    this.route.paramMap.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((params) => {
      const name = params.get('name') ?? '';
      this.name.set(name);
      this.load(name);
    });
  }

  // ------------------------------------------------------------------ loading

  private load(name: string): void {
    this.loading.set(true);
    this.loadError.set(null);
    forkJoin([this.api.catalogPackage(name), this.api.catalogPackages()]).subscribe({
      next: ([manifest, packages]) => {
        registerPackageTitles(packages);
        this.manifest.set(manifest);
        this.installed.set(packages.some((pkg) => pkg.name === name && pkg.installed === true));
        this.seedChoices(manifest);
        const draft = this.drafts.get(name);
        if (draft) {
          this.restore(draft);
        }
        this.loadSupport(manifest);
        const resumed = this.resume();
        if (!resumed && this.installed()) {
          this.stepIndex.set(this.steps().length - 1);
        }
        this.loading.set(false);
        this.enter(this.current(), false);
      },
      error: (failure: HttpErrorResponse) => {
        this.loading.set(false);
        this.loadError.set(
          failure.status === 404
            ? 'This package is not in the catalog.'
            : failure.status === 0
              ? 'Cannot reach the appliance. Check that homestead-server is running, then try again.'
              : `Could not load this package: ${typeof failure.error === 'string' && failure.error ? failure.error : failure.message}`,
        );
      },
    });
  }

  private seedChoices(manifest: CatalogManifest): void {
    const ports: Record<string, string> = {};
    for (const service of manifest.Services) {
      if (service.Exposes !== '') {
        ports[service.Name] = defaultContainerPort(manifest);
      }
    }
    this.containerPorts.set(ports);
    const mounts: Record<string, string> = {};
    for (const className of storageClasses(manifest)) {
      mounts[className] = '';
    }
    this.mountpoints.set(mounts);
    this.hostnames.set({});
    this.folders.set([]);
    this.domain.set('lan');
    this.acknowledgedEdge.set(false);
    this.zone.set('');
    this.publicIp.set('');
    this.extraNetworks.set('');
    this.stepIndex.set(0);
    this.plan.set(null);
    this.planProblem.set(null);
    this.phase.set('ready');
    this.buildOperation.set(null);
    this.installOperation.set(null);
    this.operation.set(null);
    this.routesToAdd.set([]);
    this.edgeSetup.set('idle');
    this.routeState.set('checking');
    this.reach.set(null);
  }

  private restore(draft: InstallDraft): void {
    this.domain.set(draft.domain);
    this.hostnames.set(draft.hostnames);
    this.containerPorts.set({ ...this.containerPorts(), ...draft.containerPorts });
    this.mountpoints.set({ ...this.mountpoints(), ...draft.mountpoints });
    this.folders.set(draft.folders);
    this.acknowledgedEdge.set(draft.acknowledgedEdge);
    this.zone.set(draft.zone);
    this.publicIp.set(draft.publicIp);
    this.extraNetworks.set(draft.networks);
    this.buildOperation.set(draft.buildOperation);
    this.installOperation.set(draft.installOperation);
    this.routesToAdd.set(draft.routes ?? []);
    const index = this.steps().findIndex((step) => step.id === draft.step);
    this.stepIndex.set(index >= 0 ? index : 0);
  }

  /** Picks the operation back up when the owner left while it ran. */
  private resume(): boolean {
    const install = this.installOperation();
    const build = this.buildOperation();
    if (install !== null) {
      this.phase.set('installing');
      this.goToId('install');
      this.follow(install, 'install');
      return true;
    }
    if (build !== null && this.craft()) {
      this.phase.set('building');
      this.goToId('install');
      this.follow(build, 'build');
      return true;
    }
    return false;
  }

  private loadSupport(manifest: CatalogManifest): void {
    this.api.dockerHealth().subscribe({
      next: (health) => this.health.set(health),
      error: () => this.healthUnknown.set(true),
    });
    if (storageClasses(manifest).length > 0) {
      forkJoin([
        this.api.storageRegistrations(),
        this.api.storageMounts(),
        this.api.storageAssignments(),
      ]).subscribe({
        next: ([registrations, mounts, assignments]) => {
          const registered = new Set(registrations.map((entry) => entry.Mountpoint));
          const options: MountOption[] = mounts
            // A mount can be listed twice by the appliance; offer it once.
            .filter((mount: StorageMount, index) => mounts.findIndex((m) => m.Mountpoint === mount.Mountpoint) === index)
            .filter((mount: StorageMount) => registered.has(mount.Mountpoint) && mount.Accessible && !mount.ReadOnly)
            .map((mount) => ({
              mountpoint: mount.Mountpoint,
              label: registrations.find((entry) => entry.Mountpoint === mount.Mountpoint)?.Label ?? '',
              free: mount.AvailBytes || mount.FreeBytes,
              total: mount.TotalBytes,
            }));
          this.mountOptions.set(options);
          this.assignedClasses.set(Object.fromEntries(assignments.map((entry) => [entry.Class, entry.Mountpoint])));
          const chosen = { ...this.mountpoints() };
          for (const className of storageClasses(manifest)) {
            if (chosen[className]) {
              continue;
            }
            const assigned = assignments.find((entry) => entry.Class === className)?.Mountpoint;
            if (assigned && options.some((option) => option.mountpoint === assigned)) {
              chosen[className] = assigned;
            } else if (options.length === 1) {
              chosen[className] = options[0].mountpoint;
            }
          }
          this.mountpoints.set(chosen);
        },
        error: (failure: HttpErrorResponse) =>
          this.storageError.set(
            failure.status === 0
              ? 'Cannot reach the appliance, so storage could not be listed.'
              : 'Could not list your storage. Open the Storage page to check it.',
          ),
      });
    }
    if (isCraft(manifest)) {
      this.refreshCraft(manifest);
    }
  }

  private refreshCraft(manifest: CatalogManifest): void {
    this.api.craftStatus().subscribe({
      next: (status) => {
        this.craftBuilt.set(
          status.images.some(
            (image) =>
              image.app === manifest.Name &&
              image.version === manifest.Version &&
              image.present &&
              image.match,
          ),
        );
        const verdict = status.verdicts.find((entry) => entry.app === manifest.Name);
        this.buildRefusal.set(verdict && verdict.state === 'failed' ? verdict.detail || verdict.refusal : null);
      },
      error: () => this.craftBuilt.set(null),
    });
  }

  // --------------------------------------------------------------- navigation

  protected goToId(id: StepId): void {
    const index = this.steps().findIndex((step) => step.id === id);
    if (index >= 0) {
      this.stepIndex.set(index);
    }
  }

  private enter(id: StepId, moveFocus: boolean): void {
    this.stepProblem.set(null);
    this.attempted.set(false);
    if (id === 'preflight') {
      this.replan().subscribe();
    } else if (id === 'review') {
      this.replan().subscribe();
    } else if (id === 'done') {
      this.prepareDone();
    }
    if (moveFocus) {
      this.toast.announce(`Step ${this.stepIndex() + 1} of ${this.steps().length}: ${this.headingText()}`);
      afterNextRender(() => this.heading()?.nativeElement.focus(), { injector: this.injector });
    }
  }

  protected go(index: number): void {
    this.stepIndex.set(index);
    this.enter(this.current(), true);
  }

  protected back(): void {
    if (this.stepIndex() > 0) {
      this.go(this.stepIndex() - 1);
    }
  }

  protected next(): void {
    const id = this.current();
    const problem = this.problemFor(id);
    if (problem) {
      this.attempted.set(true);
      this.stepProblem.set(problem);
      return;
    }
    this.stepProblem.set(null);
    if (id === 'storage') {
      this.replan().subscribe((ok) => {
        if (ok) {
          this.go(this.stepIndex() + 1);
        } else {
          this.stepProblem.set(this.planProblem()?.headline ?? null);
        }
      });
      return;
    }
    this.go(this.stepIndex() + 1);
  }

  /** What stops the owner leaving this step, in words. Null means go on. */
  private problemFor(id: StepId): string | null {
    switch (id) {
      case 'preflight':
        if (this.dockerDown()) {
          return 'Docker is not reachable, so nothing can be installed. Start Docker, then check again.';
        }
        if (this.planProblem()) {
          return this.planProblem()!.installed
            ? this.planProblem()!.headline
            : 'The appliance does not meet a requirement yet. Fix it, then check again.';
        }
        if (this.planning()) {
          return 'The check is still running. Wait for it to finish.';
        }
        if (this.caddyMissing() && !this.acknowledgedEdge()) {
          return 'Caddy is not running. Tick the box to continue anyway, or deploy Caddy first.';
        }
        return null;
      case 'storage': {
        if (this.mountOptions().length === 0) {
          return 'No storage is registered yet. Register a mount on the Storage page first.';
        }
        const missing = this.classes().find((className) => !this.mountpoints()[className]);
        if (missing) {
          return `Choose where ${CLASS_WORDS[missing]?.name.toLowerCase() ?? missing} goes.`;
        }
        if (this.classes().some((className) => this.classError(className))) {
          return 'Choose a mount with enough free space for the storage marked below.';
        }
        for (const row of this.folders()) {
          if (row.path.trim() === '' || row.target.trim() === '') {
            return 'Fill in both paths for each extra folder, or remove it.';
          }
          if (row.kind === 'existing' && !row.path.startsWith('/')) {
            return `The existing folder ${row.path} must be a full path that starts with a slash.`;
          }
          if (!row.target.startsWith('/')) {
            return `The path inside the app, ${row.target}, must start with a slash.`;
          }
        }
        return null;
      }
      case 'address': {
        if (this.domain().trim() === '') {
          return 'Enter a domain, such as lan.';
        }
        for (const service of this.routedServices()) {
          const port = Number(this.containerPorts()[service.Name]);
          if (!Number.isInteger(port) || port < 1 || port > 65535) {
            return `Enter the port ${service.Name} listens on, a number from 1 to 65535.`;
          }
        }
        return null;
      }
      default:
        return null;
    }
  }

  /** Free space short of the package's floor on the mount chosen for one kind of storage. */
  protected classError(className: string): string | null {
    const minimum = this.need(className);
    const option = this.freeOn(this.mountpoints()[className] ?? '');
    if (option && minimum > 0 && option.free < minimum) {
      return `${option.mountpoint} has ${size(option.free)} free and ${this.title()} needs at least ${size(minimum)}. Choose a mount with more room.`;
    }
    return null;
  }

  protected domainError(): string | null {
    return this.attempted() && this.domain().trim() === '' ? 'Enter a domain, such as lan.' : null;
  }

  protected portError(service: string): string | null {
    const port = Number(this.containerPorts()[service]);
    return this.attempted() && (!Number.isInteger(port) || port < 1 || port > 65535)
      ? `Enter the port ${service} listens on, a number from 1 to 65535.`
      : null;
  }

  // ------------------------------------------------------------------ planning

  private body(): Record<string, unknown> {
    const ports: Record<string, number> = {};
    for (const service of this.routedServices()) {
      const text = (this.containerPorts()[service.Name] ?? '').trim();
      if (text !== '') {
        ports[service.Name] = Number(text);
      }
    }
    const storage: Record<string, string> = {};
    for (const [className, mount] of Object.entries(this.mountpoints())) {
      if (mount.trim() !== '') {
        storage[className] = mount.trim();
      }
    }
    const hostnames: Record<string, string> = {};
    for (const [endpoint, host] of Object.entries(this.hostnames())) {
      if (host.trim() !== '') {
        hostnames[endpoint] = host.trim();
      }
    }
    const volumes = this.folders().map((row) =>
      row.kind === 'existing'
        ? { service: row.service, class: row.class, hostpath: row.path.trim(), target: row.target.trim(), readonly: row.readonly }
        : { service: row.service, class: row.class, subpath: row.path.trim(), target: row.target.trim(), readonly: row.readonly },
    );
    // Extra networks the app joins besides the project network and the edge
    // network, which the planner always adds. Empty keeps the default.
    const networks = this.extraNetworks()
      .split(',')
      .map((entry) => entry.trim())
      .filter((entry) => entry !== '')
      .map((entry) => ({ name: entry, external: true }));
    return {
      domain: this.domain().trim(),
      hostnames,
      container_ports: ports,
      storage,
      volumes,
      networks,
      zone: this.zone().trim(),
      public_ip: this.publicIp().trim(),
    };
  }

  private storageChosen(): boolean {
    return this.classes().every((className) => (this.mountpoints()[className] ?? '') !== '');
  }

  /** Asks the daemon for the plan with today's choices. Resolves true when it is plannable. */
  protected replan(): Observable<boolean> {
    const manifest = this.manifest();
    if (manifest === null || this.installed()) {
      return of(true);
    }
    if (!this.storageChosen()) {
      this.plan.set(null);
      this.planProblem.set(null);
      this.planDeferred.set(true);
      return of(true);
    }
    this.planDeferred.set(false);
    this.planning.set(true);
    this.planProblem.set(null);
    return this.api.planInstall(manifest.Name, this.body()).pipe(
      map((plan) => {
        this.planning.set(false);
        this.plan.set(plan);
        return true;
      }),
      catchError((failure: HttpErrorResponse) => {
        this.planning.set(false);
        this.plan.set(null);
        this.planProblem.set(explainPlanError(failure.status, failure.error));
        return of(false);
      }),
    );
  }

  protected checkAgain(): void {
    this.replan().subscribe();
    const manifest = this.manifest();
    if (manifest !== null) {
      this.api.dockerHealth().subscribe({
        next: (health) => {
          this.health.set(health);
          this.healthUnknown.set(false);
        },
        error: () => this.healthUnknown.set(true),
      });
    }
  }

  // ------------------------------------------------------------------- inputs

  protected hostnameFor(endpoint: string, index: number): string {
    return this.hostnames()[endpoint] ?? defaultHostname(this.name(), endpoint, this.domain().trim() || 'lan', index === 0);
  }

  protected setHostname(endpoint: string, value: string): void {
    this.hostnames.update((map) => ({ ...map, [endpoint]: value }));
  }

  protected setPort(service: string, value: string): void {
    this.containerPorts.update((map) => ({ ...map, [service]: value }));
  }

  protected setMount(className: string, value: string): void {
    this.mountpoints.update((map) => ({ ...map, [className]: value }));
  }

  protected addFolder(): void {
    const manifest = this.manifest();
    this.folders.update((rows) => [
      ...rows,
      {
        service: this.routedServices()[0]?.Name ?? manifest?.Services[0]?.Name ?? '',
        class: this.classes().includes('bulk') ? 'bulk' : (this.classes()[0] ?? ''),
        kind: 'existing',
        path: '',
        target: '',
        readonly: true,
      },
    ]);
  }

  protected patchFolder(index: number, patch: Partial<FolderRow>): void {
    this.folders.update((rows) => rows.map((row, position) => (position === index ? { ...row, ...patch } : row)));
  }

  protected removeFolder(index: number): void {
    this.folders.update((rows) => rows.filter((_, position) => position !== index));
  }

  // ------------------------------------------------------------ build / install

  protected startBuild(): void {
    const manifest = this.manifest();
    if (manifest === null || this.starting()) {
      return;
    }
    this.starting.set(true);
    this.failure.set(null);
    this.api.craftBuild({ app: manifest.Name }).subscribe({
      next: ({ id }) => {
        this.starting.set(false);
        this.buildOperation.set(id);
        this.operation.set(null);
        this.phase.set('building');
        this.go(this.steps().findIndex((step) => step.id === 'install'));
        this.follow(id, 'build');
      },
      error: (failure: HttpErrorResponse) => {
        this.starting.set(false);
        this.fail(failure);
      },
    });
  }

  protected startInstall(): void {
    const manifest = this.manifest();
    if (manifest === null || this.starting()) {
      return;
    }
    this.starting.set(true);
    this.failure.set(null);
    // The plan the owner reviewed is the plan that is applied, so its routes are the ones to add.
    const planned = this.plan()?.Routes as unknown as EdgeRoute[] | undefined;
    if (planned) {
      this.routesToAdd.set(planned.filter((route) => route.Exposure !== 'public'));
    }
    const body = this.body();
    this.assignChosenStorage()
      .pipe(switchMap(() => this.api.installPackage(manifest.Name, body)))
      .subscribe({
      next: ({ id }) => {
        this.starting.set(false);
        this.installOperation.set(id);
        this.operation.set(null);
        this.phase.set('installing');
        if (this.current() !== 'install') {
          this.go(this.steps().findIndex((step) => step.id === 'install'));
        }
        this.follow(id, 'install');
      },
      error: (failure: HttpErrorResponse) => {
        this.starting.set(false);
        this.fail(failure);
      },
    });
  }

  /**
   * A class with no assigned mount gets the one the owner chose here, through the
   * storage assignments API, so uninstall can name the data folders and later
   * installs default to it. A class that already has an assignment is left alone,
   * and a refusal never stops the install.
   */
  private assignChosenStorage(): Observable<unknown> {
    const assigned = this.assignedClasses();
    const calls = Object.entries(this.mountpoints())
      .filter(([className, mount]) => mount !== '' && !assigned[className])
      .map(([className, mount]) =>
        this.api
          .storageAssign({ class: className, mountpoint: mount, actor: 'owner' })
          .pipe(catchError(() => of(null))),
      );
    return calls.length > 0 ? forkJoin(calls) : of([]);
  }

  private fail(failure: HttpErrorResponse): void {
    this.failure.set(explainPlanError(failure.status, failure.error));
    this.phase.set('failed');
    if (this.current() !== 'install') {
      this.go(this.steps().findIndex((step) => step.id === 'install'));
    }
  }

  private follow(id: number, kind: 'build' | 'install'): void {
    this.api
      .operationEvents(id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (state) => {
          this.operation.set(state);
          if (!isTerminal(state.status)) {
            return;
          }
          const manifest = this.manifest();
          if (kind === 'build') {
            if (manifest) {
              this.refreshCraft(manifest);
            }
            if (state.status === 'succeeded') {
              this.phase.set('built');
              this.buildOperation.set(null);
            } else {
              this.phase.set('failed');
              this.failure.set({
                headline: `${manifest?.Title ?? 'The app'} was not built.`,
                fix: 'Check the signed craft app list and your craft signing keys on the Updates page, then try again.',
                installed: false,
              });
            }
          } else if (state.status === 'succeeded') {
            this.installed.set(true);
            // Finished: the catalog stops calling it "setting up".
            this.installOperation.set(null);
            this.phase.set('ready');
            this.go(this.steps().length - 1);
          } else {
            this.phase.set('failed');
            this.failure.set({
              headline: `${this.title()} was not installed.`,
              fix: 'Nothing is left half-installed and your data was kept. Fix the problem, then try again.',
              installed: false,
            });
          }
        },
        error: () => {
          this.failure.set({
            headline: 'Lost the live connection to the operation.',
            fix: 'The work continues on the appliance. Open the operation to see how it ended.',
            installed: false,
          });
        },
      });
  }

  /** After a failure: forget the failed operation and review the plan again. */
  protected retry(): void {
    this.installOperation.set(null);
    this.buildOperation.set(null);
    this.operation.set(null);
    this.failure.set(null);
    this.phase.set('ready');
    this.goToId('review');
    this.enter('review', true);
  }

  // -------------------------------------------------------------------- done

  /**
   * Reads the edge route table and the edge's health. An existing route for the
   * app is shown; otherwise, once the app is installed and the proxy runs, the
   * planned route is merged into the table through the edge routes API.
   */
  protected prepareDone(): void {
    if (this.routeState() === 'adding') {
      return;
    }
    this.routeState.set('checking');
    this.routeProblem.set(null);
    forkJoin([
      this.api.edgeRoutes(),
      this.api.dockerHealth().pipe(catchError(() => of(null))),
    ]).subscribe({
      next: ([{ routes }, health]) => {
        if (health) {
          this.health.set(health);
        }
        const table = routes ?? [];
        const manifest = this.manifest();
        const hosts = this.routesToAdd().map((route) => route.Host);
        const found =
          table.find((route) => hosts.includes(route.Host)) ??
          (manifest ? appRoutes(manifest, table)[0] : undefined) ??
          null;
        this.edgeRoute.set(found);
        if (found) {
          this.routeState.set('added');
          this.probe(found);
        } else if (this.caddyMissing() || (health === null && this.healthUnknown())) {
          this.routeState.set('no-edge');
        } else if (this.installed() && this.routesToAdd().length > 0) {
          this.addRoute(table);
        } else {
          this.routeState.set('none');
        }
      },
      error: () => {
        this.edgeRoute.set(null);
        this.routeState.set('none');
      },
    });
  }

  /** Merges the planned routes into the table; no existing route is dropped. */
  private addRoute(table: EdgeRoute[]): void {
    this.routeState.set('adding');
    this.api.edgeReconcile(mergeRoutes(table, this.routesToAdd())).subscribe({
      next: ({ id }) => {
        this.routeOperation.set(id);
        this.routeOpState.set(null);
        this.api
          .operationEvents(id)
          .pipe(takeUntilDestroyed(this.destroyRef))
          .subscribe({
            next: (state) => {
              this.routeOpState.set(state);
              if (!isTerminal(state.status)) {
                return;
              }
              if (state.status === 'succeeded') {
                const route = this.routesToAdd()[0] ?? null;
                this.edgeRoute.set(route);
                this.routeState.set('added');
                if (route) {
                  this.probe(route);
                }
              } else {
                this.routeState.set('failed');
                this.routeProblem.set(state.error || 'The edge refused the route.');
              }
            },
            error: () => {
              this.routeState.set('failed');
              this.routeProblem.set('Lost the live connection to the operation. Open it to see how it ended.');
            },
          });
      },
      error: (failure: HttpErrorResponse) => {
        this.routeState.set('failed');
        this.routeProblem.set(
          failure.status === 0
            ? 'Cannot reach the appliance.'
            : typeof failure.error === 'string' && failure.error.trim()
              ? failure.error.trim()
              : `The edge refused the route (${failure.status}).`,
        );
      },
    });
  }

  /** An installed app with no route: build the usual one from the manifest and the Address choices. */
  protected addMissingRoute(): void {
    const manifest = this.manifest();
    const first = this.endpoints()[0];
    if (!manifest || !first) {
      return;
    }
    const route = routeFromManifest(
      manifest,
      this.hostnameFor(first.endpoint, 0),
      Number(this.containerPorts()[first.service]),
    );
    if (route) {
      this.routesToAdd.set([route]);
      this.prepareDone();
    }
  }

  protected retryRoute(): void {
    this.prepareDone();
  }

  /**
   * Asks this device to open the address. A request that cannot complete means
   * the name does not resolve here, or this device does not trust the Homestead
   * root yet; either way the Trust page is the next step.
   */
  private probe(route: EdgeRoute): void {
    this.reach.set('checking');
    if (typeof fetch !== 'function') {
      this.reach.set(null);
      return;
    }
    fetch(this.routeUrl(route), { mode: 'no-cors', cache: 'no-store' }).then(
      () => this.reach.set('ok'),
      () => this.reach.set('failed'),
    );
  }

  // ---------------------------------------------------------------- the edge

  protected askEdgeSetup(): void {
    this.edgeProblem.set(null);
    this.edgeDialog.set(true);
  }

  protected cancelEdgeSetup(): void {
    this.edgeDialog.set(false);
  }

  /** Deploys Caddy through the existing edge operation; progress shows here and in the rail. */
  protected confirmEdgeSetup(): void {
    this.edgeSetup.set('starting');
    this.api.edgeDeploy().subscribe({
      next: ({ id }) => {
        this.edgeDialog.set(false);
        this.edgeOperation.set(id);
        this.edgeOpState.set(null);
        this.edgeSetup.set('running');
        this.api
          .operationEvents(id)
          .pipe(takeUntilDestroyed(this.destroyRef))
          .subscribe({
            next: (state) => {
              this.edgeOpState.set(state);
              if (!isTerminal(state.status)) {
                return;
              }
              if (state.status === 'succeeded') {
                this.edgeSetup.set('done');
                this.api.dockerHealth().subscribe({
                  next: (health) => {
                    this.health.set(health);
                    if (this.current() === 'done') {
                      this.prepareDone();
                    }
                  },
                });
              } else {
                this.edgeSetup.set('failed');
                this.edgeProblem.set(state.error || 'The edge could not be set up.');
              }
            },
            error: () => {
              this.edgeSetup.set('failed');
              this.edgeProblem.set('Lost the live connection to the operation. Open it to see how it ended.');
            },
          });
      },
      error: (failure: HttpErrorResponse) => {
        this.edgeDialog.set(false);
        this.edgeSetup.set('failed');
        this.edgeProblem.set(
          failure.status === 0
            ? 'Cannot reach the appliance.'
            : typeof failure.error === 'string' && failure.error.trim()
              ? failure.error.trim()
              : `The edge could not be set up (${failure.status}).`,
        );
      },
    });
  }

  protected routeUrl(route: EdgeRoute): string {
    return `${route.TLS === 'off' ? 'http' : 'https'}://${route.Host}`;
  }

  protected readonly plannedRoutes = computed(() => this.routesToAdd());

  /** Called when the owner finishes with this wizard: the draft goes. */
  protected finish(): void {
    this.drafts.clear(this.name());
  }

  protected onUninstalled(): void {
    this.installed.set(false);
    this.drafts.clear(this.name());
  }

  // ----------------------------------------------------------------- helpers

  protected readonly memory = memory;
  protected readonly size = size;

  protected classWord(className: string): string {
    return CLASS_WORDS[className]?.name ?? className;
  }

  protected classHelp(className: string): string {
    return CLASS_WORDS[className]?.help ?? '';
  }

  protected need(className: string): number {
    return this.manifest()?.Requirements.Storage.find((entry) => entry.Class === className)?.MinBytes ?? 0;
  }

  /** The daemon's notes start lowercase; they are shown as sentences. */
  protected sentence(note: string): string {
    return note.charAt(0).toUpperCase() + note.slice(1);
  }

  protected exposureWords(exposure: string): string {
    switch (exposure) {
      case 'local':
        return 'Local network only';
      case 'overlay':
        return 'Your private overlay network';
      case 'public':
        return 'The public internet';
      default:
        return exposure;
    }
  }

  protected freeOn(mountpoint: string): MountOption | undefined {
    return this.mountOptions().find((entry) => entry.mountpoint === mountpoint);
  }

  protected stepState(index: number): 'done' | 'current' | 'todo' {
    return index < this.stepIndex() ? 'done' : index === this.stepIndex() ? 'current' : 'todo';
  }
}
