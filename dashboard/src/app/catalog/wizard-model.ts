import { curatedFor } from './curated-copy';
import { CatalogManifest, DockerHealth, InstallPlan } from '../core/api.service';

/** The steps of the install wizard (S285), in order. A step that does not apply is left out. */
export type StepId =
  | 'about'
  | 'preflight'
  | 'storage'
  | 'address'
  | 'sign-in'
  | 'review'
  | 'install'
  | 'done';

export interface StepDef {
  id: StepId;
  /** Short name shown in the step list. */
  label: string;
}

const LABELS: Record<StepId, string> = {
  about: 'What it is',
  preflight: 'Requirements',
  storage: 'Storage',
  address: 'Address',
  'sign-in': 'Secrets and sign-in',
  review: 'Review',
  install: 'Install',
  done: 'Done',
};

/** Craft apps are static sites built on the appliance from a signed package (ADR-028). */
export function isCraft(manifest: CatalogManifest): boolean {
  return manifest.Services.some((service) => service.Image.startsWith('homestead/craft-'));
}

/** The storage classes the package needs: declared floors plus every persistent data path. */
export function storageClasses(manifest: CatalogManifest): string[] {
  const classes: string[] = [];
  const add = (name: string) => {
    if (name && !classes.includes(name)) {
      classes.push(name);
    }
  };
  for (const need of manifest.Requirements.Storage) {
    add(need.Class);
  }
  for (const service of manifest.Services) {
    for (const data of service.Data ?? []) {
      if (!data.Ephemeral) {
        add(data.Class);
      }
    }
  }
  return classes;
}

/** The single sign-on class; an empty class reads as unsupported. */
export function ssoClass(manifest: CatalogManifest): 'unsupported' | 'optional' | 'recommended' | 'required' {
  const raw = manifest.SSO?.Class;
  return raw === 'optional' || raw === 'recommended' || raw === 'required' ? raw : 'unsupported';
}

/** Secrets and sign-in is shown where the manifest declares secrets or supports sign-in through a provider. */
export function hasSignInStep(manifest: CatalogManifest): boolean {
  return (manifest.Secrets ?? []).length > 0 || ssoClass(manifest) !== 'unsupported';
}

/** The step sequence for one package. */
export function stepsFor(manifest: CatalogManifest): StepDef[] {
  const ids: StepId[] = ['about', 'preflight'];
  if (storageClasses(manifest).length > 0) {
    ids.push('storage');
  }
  ids.push('address');
  if (hasSignInStep(manifest)) {
    ids.push('sign-in');
  }
  ids.push('review', 'install', 'done');
  return ids.map((id) => ({ id, label: LABELS[id] }));
}

/** Bytes in binary units, one decimal. */
export function size(bytes: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${unit === 0 ? value : value.toFixed(1)} ${units[unit]}`;
}

/** Megabytes the way a person says them: 512 MB, 2 GB. */
export function memory(mb: number): string {
  return mb >= 1024 ? `${Number((mb / 1024).toFixed(1))} GB` : `${mb} MB`;
}

/** The mirror of the daemon's default hostnames: the first endpoint is the app name. */
export function defaultHostname(name: string, endpoint: string, domain: string, first: boolean): string {
  return first ? `${name}.${domain}` : `${endpoint}-${name}.${domain}`;
}

/** The endpoints that get a route, in the order the daemon plans them. */
export function endpointsOf(manifest: CatalogManifest): { endpoint: string; service: string }[] {
  const byEndpoint = new Map<string, string>();
  for (const service of manifest.Services) {
    if (service.Exposes) {
      byEndpoint.set(service.Exposes, service.Name);
    }
  }
  const order = manifest.Exposure.Endpoints.map((entry) => entry.Endpoint);
  const names = order.length > 0 ? order : [...byEndpoint.keys()];
  return names.map((endpoint) => ({ endpoint, service: byEndpoint.get(endpoint) ?? '' }));
}

/** The container port the planner needs for a routed service. */
export function defaultContainerPort(manifest: CatalogManifest): string {
  const declared = manifest.Requirements.Ports ?? [];
  if (declared.length > 0) {
    return String(declared[0]);
  }
  return curatedFor(manifest.Name)?.containerPort ?? (isCraft(manifest) ? '8080' : '');
}

export interface EdgeReadiness {
  /** Docker answered. */
  dockerReachable: boolean;
  dockerVersion: string;
  /** The Caddy edge proxy container is running. */
  caddyRunning: boolean;
  /** The shared edge network already exists (install creates it when it does not). */
  networkExists: boolean;
}

/** What the Docker health answer says about the edge, in the owner's terms. */
export function edgeReadiness(health: DockerHealth): EdgeReadiness {
  const caddy = (health.containers ?? []).some(
    (container) =>
      container.state === 'running' &&
      container.names.some((name) => name.replace(/^\//, '') === 'homestead-caddy'),
  );
  return {
    dockerReachable: health.reachable,
    dockerVersion: health.version ?? '',
    caddyRunning: caddy,
    networkExists: (health.networks ?? []).some((network) => network.name === 'homestead-edge'),
  };
}

export interface PlanProblem {
  /** What happened, from the daemon's own words. */
  headline: string;
  /** How to fix it. */
  fix: string;
  /** Install is already done: manage it instead of retrying. */
  installed: boolean;
}

/** Turns a refused plan into what happened and how to fix it. */
export function explainPlanError(status: number, raw: unknown): PlanProblem {
  const text = (typeof raw === 'string' ? raw : '').replace(/^catalog:\s*/, '').trim();
  if (status === 409) {
    return {
      headline: text || 'This app is already installed.',
      fix: 'Manage the installed app instead. Installing again would replace its data mounts.',
      installed: true,
    };
  }
  if (status === 0) {
    return {
      headline: 'Cannot reach the appliance.',
      fix: 'Check that homestead-server is running, then check again.',
      installed: false,
    };
  }
  const headline = text || 'The appliance refused this plan.';
  const rules: [RegExp, string][] = [
    [/memory:/, 'This appliance has less memory than the app needs. Stop other apps or choose a smaller one.'],
    [/cpu:/, 'This appliance has fewer processor cores than the app needs.'],
    [/architecture/, 'This app does not run on this appliance’s processor type.'],
    [/gpu:|needs a GPU/, 'This app needs a graphics card this appliance does not have.'],
    [/port:|already bound/, 'Another program is using a port the app needs. Stop that program, then check again.'],
    [/storage:|offers \d+ bytes/, 'Free up space on that storage, or choose a mount with more room in the Storage step.'],
    [/has no mountpoint|no space data/, 'Choose a mount for each kind of storage in the Storage step.'],
    [/not a mounted filesystem/, 'Mount that storage first, register it on the Storage page, then check again.'],
    [/not accessible|read-only/, 'Choose a mount that can be written to in the Storage step.'],
    [/outside the .* mount|host path/, 'Choose a folder that exists inside the mount you picked.'],
    [/domain must not be empty/, 'Enter a domain in the Address step.'],
    [/route .*|hostname/, 'Check the hostnames in the Address step: lowercase letters, numbers, dots and dashes.'],
    [/container port/, 'Enter the port the app listens on inside its container, in the Address step.'],
  ];
  const fix = rules.find(([pattern]) => pattern.test(text))?.[1];
  return {
    headline,
    fix: fix ?? 'Fix the problem above, then check again.',
    installed: false,
  };
}

/** A short sentence for what Homestead will create, from the plan. */
export function planSummary(plan: InstallPlan): { created: string[]; changed: string[]; leftAlone: string[] } {
  const created: string[] = [];
  const changed: string[] = [];
  for (const dir of plan.Directories ?? []) {
    const path = dir.Directory?.Path ?? '';
    created.push(`Folder ${dir.Mountpoint}/${path}`.replace(/\/$/, ''));
  }
  if (plan.EdgeNetwork) {
    created.push('The homestead-edge network, if it does not exist yet');
  }
  for (const route of plan.Routes ?? []) {
    created.push(`Address ${route.Host} (${route.Exposure === 'local' ? 'local network only' : route.Exposure})`);
  }
  for (const issue of plan.TLS ?? []) {
    created.push(
      issue.Issuer === 'internal'
        ? `Certificate for ${issue.Host} from your Homestead internal CA`
        : `Public certificate for ${issue.Host}`,
    );
  }
  for (const record of plan.DNS ?? []) {
    changed.push(`DNS record ${record.Name} (${record.Type}) pointing to ${record.Value}`);
  }
  for (const policy of plan.Firewall ?? []) {
    changed.push(`Firewall rule for ${policy.Service.Name}`);
  }
  return {
    created,
    changed,
    leftAlone: [
      'Your other apps and their data',
      'Your other addresses; the new address is merged into the route table, never replacing it',
      'Folders you point the app at; they are mounted, never changed',
    ],
  };
}
