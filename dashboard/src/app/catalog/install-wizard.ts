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
import { SessionService } from '../auth/session.service';
import {
  ApiService,
  AppAccessChoice,
  AppAccessRole,
  AppPublicPlan,
  AppPublicRequest,
  AppPublicStatus,
  AppReach,
  AppSetup as AppSetupView,
  CatalogManifest,
  DockerHealth,
  EdgeRoute,
  InstallPlan,
  InstallSetupApproval,
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
import { coverageLamp, coverageRule } from '../core/coverage';
import { ApprovedPlan, approvedPlan, Permit } from '../ui/permit';
import { publicError } from './app-public';
import { appRoutes, mergeRoutes, routeFromManifest } from './app-routes';
import { AppSetup } from './app-setup';
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
  localAlias,
  memory,
  planSummary,
  publishByDefault,
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
function helpFor(id: StepId, title: string, byPath = false): string {
  switch (id) {
    case 'about':
      return `Here is what ${title} is and what it does on your appliance.`;
    case 'preflight':
      return `Homestead checks that this appliance can run ${title} before anything changes.`;
    case 'storage':
      return `Choose where ${title} keeps its files; only storage you have already mounted and registered is listed. A kind of storage with no default yet gets your choice as its default.`;
    case 'address':
      return byPath
        ? `Where ${title} answers on your network and, if you publish it, on the internet.`
        : `Choose the name you type to reach ${title} on your network.`;
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
    AppSetup,
    Permit,
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
  /** The label of the button that hands back to the embedding page (S292). */
  readonly closeLabel = input('Back to the app list');
  readonly appName = input('');
  readonly closed = output<void>();
  /** Opened from the setup walkthrough (S287): offer the way back. */
  protected readonly fromSetup = this.route.snapshot?.queryParamMap?.get('from') === 'setup';
  private readonly drafts = inject(InstallDrafts);
  private readonly toast = inject(ToastService);
  private readonly destroyRef = inject(DestroyRef);
  private readonly injector = inject(Injector);
  private readonly sessions = inject(SessionService);

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
  protected readonly routeState = signal<'checking' | 'adding' | 'added' | 'failed' | 'no-edge' | 'setup' | 'none'>('checking');
  protected readonly routeProblem = signal<string | null>(null);

  /** S298: "Sign in with Authentik", checked by default when the install plan offers it. */
  protected readonly signInWanted = signal(true);
  protected readonly signInPreview = computed(() => this.plan()?.sign_in ?? null);
  /** S326: the craft apps the install's setup links to Nextcloud for files. */
  protected readonly filesLink = computed(() => this.plan()?.files_link ?? null);
  /** S390: each managed person must have an explicit access choice. */
  protected readonly accessRoles = signal<Record<number, AppAccessRole>>({});
  private readonly accessRosterKey = signal<string | null>(null);
  protected readonly accessPlan = computed(() => this.plan()?.access ?? null);
  protected readonly accessChoices = computed<AppAccessChoice[]>(() =>
    this.accessPlan()?.mode === 'managed'
      ? this.accessPlan()!.people.flatMap((person) => {
          if (person.state !== 'active') {
            return [{ person_id: person.id, role: 'none' as const }];
          }
          const role = this.accessRoles()[person.id];
          return role ? [{ person_id: person.id, role }] : [];
        })
      : [],
  );
  protected readonly accessSelectionComplete = computed(() => {
    const access = this.accessPlan();
    return access !== null && access.mode !== 'unavailable' && (access.mode !== 'managed' || access.people.every(
      (person) => person.state !== 'active' || Object.hasOwn(this.accessRoles(), person.id),
    ));
  });
  protected readonly accessUnavailable = computed(() => this.accessPlan()?.mode === 'unavailable');
  protected readonly managedAccess = computed(() => this.accessPlan()?.mode === 'managed');
  protected readonly hasAdminAccess = computed(() => this.accessChoices().some((choice) => choice.role === 'admin'));
  protected readonly managedSignInRequired = computed(() => this.accessPlan()?.mode === 'managed');
  protected readonly managedSignInReady = computed(() =>
    !this.managedSignInRequired() || (this.signInPreview()?.offered === true && this.signInPreview()?.plan !== undefined),
  );

  // S313: the public address, planned and approved with the install and
  // applied by its digest once the app is installed.
  protected readonly resolverOn = signal<boolean | null>(null);
  protected readonly publicStatus = signal<AppPublicStatus | null>(null);
  /** The owner's choice on "Also publish at"; null keeps the default. */
  protected readonly publishChoice = signal<boolean | null>(null);
  protected readonly publicName = signal('');
  protected readonly publicPlan = signal<AppPublicPlan | null>(null);
  protected readonly publicPlanning = signal(false);
  protected readonly publicPlanProblem = signal<string | null>(null);
  protected readonly publicApproved = signal<{ request: AppPublicRequest; digest: string; hostname: string } | null>(null);
  protected readonly installStamp = signal<ApprovedPlan | null>(null);
  protected readonly publicStamp = signal<ApprovedPlan | null>(null);
  /** S324: the setup approved with the install, as the appliance last reported it. */
  protected readonly setupView = signal<AppSetupView | null>(null);
  /** The approved public address was published by the setup. */
  protected readonly publicDone = computed(() => this.setupView()?.steps.some((step) => step.name === 'publish' && step.state === 'done') ?? false);
  protected readonly reachStatus = signal<AppReach | null>(null);
  protected readonly reachChecking = signal(false);
  protected readonly reachFailed = signal(false);
  protected readonly tunnelReady = computed(() => !!this.publicStatus()?.tunnel_id);
  /** The appliance's local names, the dashboard route's (S315): a path app answers under them. */
  protected readonly applianceNames = signal<string[]>(['homestead.lan', 'homestead.local']);
  /** The path an app served by path answers at (ADR-044), or null for an app with a name of its own. */
  protected readonly appPath = computed(() => {
    const m = this.manifest();
    return m && m.Serve === 'path' ? m.Path || `/apps/${m.Name}/` : null;
  });
  /** The local addresses of a path app: its path under each appliance name. */
  protected readonly pathAddresses = computed(() => {
    const path = this.appPath();
    return path ? this.applianceNames().map((name) => `https://${name}${path}`) : [];
  });
  /** A path app is published on the dashboard's public name; without one there is nothing to publish it on. */
  protected readonly pathNeedsServer = computed(
    () => this.appPath() !== null && this.tunnelReady() && !this.publicStatus()?.proposed_url,
  );
  /** A tunnel exists and this app may be published with its install (not Authentik, whose name is the tunnel's). */
  protected readonly publishable = computed(
    () =>
      this.tunnelReady() &&
      this.name() !== 'authentik' &&
      this.endpoints().length > 0 &&
      !this.installed() &&
      !this.pathNeedsServer(),
  );
  /** What the public address reads as: a path app's URL, or the public name. */
  protected readonly publicShown = computed(() => {
    const approved = this.publicApproved();
    const plan = this.publicPlan();
    if (plan?.path && plan.url && (!approved || approved.digest === plan.digest)) {
      return plan.url;
    }
    if (this.appPath() && this.publicStatus()?.proposed_url) {
      return this.publicStatus()!.proposed_url!;
    }
    return approved?.hostname ?? this.publicHost();
  });
  protected readonly publish = computed(
    () => this.publishable() && (this.publishChoice() ?? publishByDefault(this.signInPreview()?.offered === true)),
  );
  protected readonly publicHost = computed(
    () => this.publicName().trim().toLowerCase() || this.publicStatus()?.proposed || '',
  );
  /** The S312 certificate lamp for the name, once a plan for that exact name came back. */
  protected readonly publicLamp = computed(() => {
    const plan = this.publicPlan();
    return plan && plan.hostname === this.publicHost() ? coverageLamp(plan.coverage, plan.hostname) : undefined;
  });
  protected readonly coverageRule = coverageRule;
  protected readonly localAlias = localAlias;
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
  /** The masthead sentence: where the owner is in the install. */
  protected readonly fact = computed(() => {
    const steps = this.steps();
    if (this.current() === 'done') {
      return `${this.title()} is installed.`;
    }
    const step = steps[this.stepIndex()];
    return step ? `Step ${this.stepIndex() + 1} of ${steps.length}: ${step.label}.` : '';
  });
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
    return helpFor(this.current(), this.title(), this.appPath() !== null);
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
        const path = `${mount}/${manifest.Name}/${data.Subpath}`;
        // Services that share a folder (Authentik's server and worker) list it once (S292).
        if (!data.Ephemeral && mount && !rows.some((row) => row.path === path)) {
          rows.push({ path, class: data.Class });
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
        publish: this.publishChoice(),
        publicName: this.publicName(),
        publicApproved: this.publicApproved(),
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
        ports[service.Name] = defaultContainerPort(manifest, service.Name);
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
    this.publishChoice.set(null);
    this.publicName.set('');
    this.publicPlan.set(null);
    this.publicPlanProblem.set(null);
    this.publicApproved.set(null);
    this.installStamp.set(null);
    this.publicStamp.set(null);
    this.setupView.set(null);
    this.reachStatus.set(null);
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
    this.publishChoice.set(draft.publish ?? null);
    this.publicName.set(draft.publicName ?? '');
    this.publicApproved.set(draft.publicApproved ?? null);
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
    // S313: whether `.lan` resolves without a router entry, and whether a
    // tunnel can publish the app with its install. Neither stops the wizard.
    this.api
      .names()
      .pipe(catchError(() => of(null)))
      .subscribe((names) => this.resolverOn.set(names ? names.resolver.enabled : null));
    // S315: the names a path app answers under are the dashboard route's.
    if (manifest.Serve === 'path') {
      this.api
        .edgeRoutes()
        .pipe(catchError(() => of(null)))
        .subscribe((table) => {
          const dash = (table?.routes ?? []).find((route) => route.Dashboard);
          if (dash) {
            this.applianceNames.set([dash.Host, ...(dash.Aliases ?? [])]);
          }
        });
    }
    this.api
      .appPublic(manifest.Name)
      .pipe(catchError(() => of(null)))
      .subscribe((status) => {
        this.publicStatus.set(status);
        if (this.current() === 'address' && this.publish()) {
          this.checkPublicName();
        }
      });
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
    } else if (id === 'address') {
      if (this.publish() && !this.publicPlan()) {
        this.checkPublicName();
      }
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
        if (this.publish() && this.publicHost() === '') {
          return 'Enter the public name, or turn off Also publish at.';
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
      // S313: the sign-in preview's redirect addresses include the public name.
      ...(this.publish() && this.publicHost() ? { public_name: this.publicHost() } : {}),
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
      switchMap((plan) => {
        this.planning.set(false);
        this.updateAccessRoster(plan);
        this.plan.set(plan);
        // The review shows the public address permit beside the install's.
        if (this.current() !== 'review') {
          return of(true);
        }
        const route = (plan.Routes ?? []).find((entry) => entry.Exposure !== 'public')?.Host ?? '';
        return this.planPublic(route).pipe(map(() => true));
      }),
      catchError((failure: HttpErrorResponse) => {
        this.planning.set(false);
        this.plan.set(null);
        this.planProblem.set(explainPlanError(failure.status, failure.error));
        return of(false);
      }),
    );
  }

  /**
   * Plans the public address for the route the install creates (S313),
   * through the app public plan route with before_install. Nothing changes;
   * a refusal is shown, and the owner may turn publishing off.
   */
  private planPublic(route: string): Observable<AppPublicPlan | null> {
    const manifest = this.manifest();
    if (!manifest || !this.publish() || !route || !this.publicHost()) {
      this.publicPlan.set(null);
      this.publicPlanProblem.set(null);
      return of(null);
    }
    this.publicPlanning.set(true);
    this.publicPlanProblem.set(null);
    const request: AppPublicRequest = { enabled: true, hostname: this.publicHost(), route, before_install: true,
      ...(this.managedAccess() ? { anticipate_sign_in: true } : {}) };
    return this.api.appPublicPlan(manifest.Name, request).pipe(
      map((plan) => {
        this.publicPlanning.set(false);
        this.publicPlan.set(plan);
        return plan;
      }),
      catchError((failure: HttpErrorResponse) => {
        this.publicPlanning.set(false);
        this.publicPlan.set(null);
        this.publicPlanProblem.set(publicError(failure));
        return of(null);
      }),
    );
  }

  /** The address step's check of the public name: its certificate lamp and any refusal. */
  protected checkPublicName(): void {
    const first = this.endpoints()[0];
    if (first) {
      this.planPublic(this.hostnameFor(first.endpoint, 0)).subscribe();
    }
  }

  protected setPublish(on: boolean): void {
    this.publishChoice.set(on);
    this.publicPlan.set(null);
    this.publicPlanProblem.set(null);
    if (on) {
      this.checkPublicName();
    }
  }

  protected setPublicName(value: string): void {
    this.publicName.set(value);
    this.publicPlan.set(null);
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
    // One approval covers both plans the owner reviewed (S313): the install
    // by its digest now, the public address by its digest once installed.
    const reviewed = this.plan();
    const by = this.sessions.username();
    const body: Record<string, unknown> = { ...this.body(), ...(reviewed?.digest ? { digest: reviewed.digest } : {}) };
    this.installStamp.set(
      reviewed?.digest
        ? approvedPlan(`Install ${manifest.Title}`, reviewed.digest, `Install ${manifest.Title} at ${(this.appPath() ? this.pathAddresses() : this.routesToAdd().map((route) => route.Host)).join(', ') || 'no address'}.`, by)
        : null,
    );
    const published = this.publish() ? this.publicPlan() : null;
    this.publicApproved.set(
      published
        ? { request: { enabled: true, hostname: published.hostname, route: published.route }, digest: published.digest, hostname: published.hostname }
        : null,
    );
    this.publicStamp.set(
      published
        ? published.path
          ? approvedPlan(`Publish ${published.url}`, published.digest, `Publish ${published.url} through the tunnel ${published.tunnel}, under the dashboard's public name, once ${manifest.Title} is installed.`, by)
          : approvedPlan(`Publish ${published.hostname}`, published.digest, `Publish ${published.hostname} to ${published.route} through the tunnel ${published.tunnel}, once ${manifest.Title} is installed.`, by)
        : null,
    );
    // S324 (ADR-048): the approvals travel with the install and the
    // appliance runs them once it succeeds, so closing this page changes
    // nothing. Sign-in needs the kept token; without one the appliance
    // records it as skipped and says how to set it up later.
    const setup: InstallSetupApproval = {};
    if (published) {
      setup.public = { hostname: published.hostname, route: published.route, digest: published.digest };
    }
    const signIn = this.signInPreview();
    if (signIn?.offered && signIn.plan && (this.managedSignInRequired() || this.signInWanted())) {
      setup.sign_in = { digest: signIn.plan.digest };
    }
    // S326 (ADR-048 §4): the Nextcloud link the review lists runs with the
    // rest, after the address; it needs no approval of its own.
    const filesLink = this.filesLink();
    if (filesLink) {
      setup.files_link = { digest: filesLink.digest };
    }
    body['setup'] = setup;
    this.setupView.set(null);
    const access = this.accessPlan();
    if (!access || access.mode === 'unavailable' || !this.accessSelectionComplete() ||
      (access.mode === 'managed' && (!signIn?.offered || !signIn.plan))) {
      this.starting.set(false);
      this.failure.set({
        headline: 'App access is not ready.',
        fix: access?.reason ?? (access?.mode === 'managed'
          ? 'Choose an access role for every active person and keep managed sign-in enabled.'
          : 'Review the app access plan before installing.'),
        installed: false,
      });
      return;
    }
    const accessChoices = this.accessChoices();
    this.assignChosenStorage()
      .pipe(
        switchMap(() => {
          if (access.mode !== 'managed') {
            return this.api.installPackage(manifest.Name, body);
          }
          return this.api.planCatalogAccess(manifest.Name, { ...this.body(), choices: accessChoices }).pipe(
            switchMap(({ digest }) => {
              setup.access = { digest, choices: accessChoices };
              body['setup'] = setup;
              return this.hasAdminAccess()
                ? this.api.installPackageAdmin(manifest.Name, body)
                : this.api.installPackage(manifest.Name, body);
            }),
          );
        }),
      )
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
  protected prepareDone(addMissing = false): void {
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
          this.afterRoute(found);
        } else if (this.caddyMissing() || (health === null && this.healthUnknown())) {
          this.routeState.set('no-edge');
        } else if (this.installed() && this.routesToAdd().length > 0) {
          // The setup adds the address; without one on record, add it here.
          if (addMissing || this.setupView()?.status === 'none') {
            this.addRoute(table);
          } else {
            this.routeState.set('setup');
          }
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

  /** The app has its address: check it from this device and from the appliance. */
  private afterRoute(route: EdgeRoute | null): void {
    if (route) {
      this.probe(route);
    }
    this.checkReach();
  }

  /**
   * The setup approved with the install runs on the appliance (S324,
   * ADR-048): the address, the public address, sign-in and people. Once it
   * is no longer running, read the address it added. An install with no
   * setup on record (one started before S324) adds the address here.
   */
  protected onSetupSettled(setup: AppSetupView): void {
    this.setupView.set(setup);
    if (this.routeState() === 'setup') {
      this.prepareDone();
    } else if (this.routeState() === 'added' && setup.status !== 'none') {
      // The public address may answer now.
      this.checkReach();
    }
  }

  /** Asks the appliance whether each address answers (S313). Advisory: it never changes the install. */
  protected checkReach(): void {
    const manifest = this.manifest();
    if (!manifest || !this.installed()) {
      return;
    }
    this.reachChecking.set(true);
    this.reachFailed.set(false);
    this.api.appReach(manifest.Name).subscribe({
      next: (status) => {
        this.reachChecking.set(false);
        this.reachStatus.set(status);
      },
      error: () => {
        this.reachChecking.set(false);
        this.reachFailed.set(true);
      },
    });
  }

  protected setAccessRole(id: number, role: AppAccessRole): void {
    const person = this.accessPlan()?.people.find((entry) => entry.id === id);
    if (!person || person.state !== 'active' || !['none', 'user', 'admin'].includes(role) ||
      (role === 'admin' && !this.accessPlan()?.admin_supported)) {
      return;
    }
    this.accessRoles.update((roles) => ({ ...roles, [id]: role }));
  }

  private updateAccessRoster(plan: InstallPlan): void {
    const access = plan.access;
    const key = access
      ? JSON.stringify([access.mode, access.people.map(({ id, display_name, username, state }) => [id, display_name, username, state])])
      : null;
    if (this.accessRosterKey() !== key) {
      this.accessRoles.set({});
      this.accessRosterKey.set(key);
    }
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
                this.afterRoute(route);
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
      // The owner asked for it here, so the page adds it, setup or not.
      this.prepareDone(true);
    }
  }

  protected retryRoute(): void {
    this.prepareDone(true);
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
    if (route.Path) {
      // S315: a path app answers under the appliance's name.
      return `https://${this.applianceNames()[0]}${route.Path}`;
    }
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
