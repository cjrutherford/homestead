import { describe, expect, it } from 'vitest';
import { CatalogManifest, DockerHealth, InstallPlan } from '../core/api.service';
import {
  edgeReadiness,
  endpointsOf,
  explainPlanError,
  hasSignInStep,
  isCraft,
  memory,
  planSummary,
  size,
  ssoClass,
  stepsFor,
  storageClasses,
} from './wizard-model';

function manifest(overrides: Partial<CatalogManifest> = {}): CatalogManifest {
  return {
    Name: 'app',
    Version: '1.0',
    Title: 'App',
    Blurb: 'An app.',
    Description: 'Does things.',
    Category: 'files',
    Services: [{ Name: 'app', Image: 'example/app:1.0', Exposes: 'web' }],
    Requirements: { MemoryMB: 512, Storage: [], Ports: [] },
    Exposure: { Package: 'app', Endpoints: [{ Endpoint: 'web', Recommended: 'local', AllowWider: false, Warnings: [] }] },
    BackupPaths: [],
    ...overrides,
  };
}

const ids = (m: CatalogManifest) => stepsFor(m).map((step) => step.id);

describe('wizard step model', () => {
  it('leaves out storage and sign-in for a stateless craft app', () => {
    const craft = manifest({
      Services: [{ Name: 'gridcraft', Image: 'homestead/craft-gridcraft:0.7.0', Exposes: 'web', Data: [{ Target: '/tmp', Class: '', Subpath: '', Ephemeral: true }] }],
      SSO: { Class: 'unsupported' },
    });
    expect(isCraft(craft)).toBe(true);
    expect(ids(craft)).toEqual(['about', 'preflight', 'address', 'review', 'install', 'done']);
  });

  it('adds storage when the manifest declares storage', () => {
    const withStorage = manifest({ Requirements: { MemoryMB: 1, Storage: [{ Class: 'bulk', MinBytes: 1 }], Ports: [] } });
    expect(ids(withStorage)).toEqual(['about', 'preflight', 'storage', 'address', 'review', 'install', 'done']);
  });

  it('adds storage for persistent data paths even without a floor', () => {
    const data = manifest({
      Services: [{ Name: 'app', Image: 'x:1', Exposes: 'web', Data: [{ Target: '/d', Class: 'application', Subpath: 'd' }] }],
    });
    expect(storageClasses(data)).toEqual(['application']);
    expect(ids(data)).toContain('storage');
  });

  it('adds secrets and sign-in when sso is optional, with no storage', () => {
    const optional = manifest({ SSO: { Class: 'optional', Protocols: ['oidc'] } });
    expect(ssoClass(optional)).toBe('optional');
    expect(ids(optional)).toEqual(['about', 'preflight', 'address', 'sign-in', 'review', 'install', 'done']);
  });

  it('keeps the step for an unsupported sso class when the package has secrets', () => {
    const authentik = manifest({ SSO: { Class: 'unsupported' }, Secrets: [{ Name: 'db_password', Immutable: false }] });
    expect(hasSignInStep(authentik)).toBe(true);
    expect(hasSignInStep(manifest({ SSO: { Class: '' } }))).toBe(false);
  });

  it('reads an empty sso class as unsupported', () => {
    expect(ssoClass(manifest({ SSO: { Class: '' } }))).toBe('unsupported');
    expect(ssoClass(manifest())).toBe('unsupported');
  });

  it('orders endpoints as the daemon does and names them from services', () => {
    const two = manifest({
      Services: [
        { Name: 'server', Image: 'x:1', Exposes: 'web' },
        { Name: 'admin', Image: 'x:1', Exposes: 'admin' },
      ],
      Exposure: {
        Package: 'app',
        Endpoints: [
          { Endpoint: 'web', Recommended: 'local', AllowWider: true, Warnings: [] },
          { Endpoint: 'admin', Recommended: 'local', AllowWider: false, Warnings: [] },
        ],
      },
    });
    expect(endpointsOf(two)).toEqual([
      { endpoint: 'web', service: 'server' },
      { endpoint: 'admin', service: 'admin' },
    ]);
  });
});

describe('wizard formatting', () => {
  it('says memory and bytes the way a person does', () => {
    expect(memory(512)).toBe('512 MB');
    expect(memory(2048)).toBe('2 GB');
    expect(size(53687091200)).toBe('50.0 GiB');
    expect(size(0)).toBe('0 B');
  });
});

describe('edge readiness', () => {
  const base: DockerHealth = { reachable: true, version: '26.1', containers: [], networks: [] };

  it('reads Caddy and the edge network from the Docker health', () => {
    const ready = edgeReadiness({
      ...base,
      containers: [{ names: ['/homestead-caddy'], state: 'running' }],
      networks: [{ name: 'homestead-edge' }],
    });
    expect(ready).toEqual({ dockerReachable: true, dockerVersion: '26.1', caddyRunning: true, networkExists: true });
  });

  it('reports Caddy missing and a stopped Caddy the same way', () => {
    expect(edgeReadiness(base).caddyRunning).toBe(false);
    expect(
      edgeReadiness({ ...base, containers: [{ names: ['/homestead-caddy'], state: 'exited' }] }).caddyRunning,
    ).toBe(false);
    expect(edgeReadiness({ ...base, containers: null, networks: null }).networkExists).toBe(false);
  });
});

describe('plan errors', () => {
  it('says what happened and how to fix it', () => {
    const memoryProblem = explainPlanError(
      422,
      'catalog: requirements unmet for "immich": memory: host offers 1024 MB for 4096 required',
    );
    expect(memoryProblem.headline).toBe('requirements unmet for "immich": memory: host offers 1024 MB for 4096 required');
    expect(memoryProblem.fix).toContain('less memory');
    expect(explainPlanError(422, 'catalog: x: port 80 already bound on the host').fix).toContain('Another program');
    expect(explainPlanError(422, '/mnt/x is not a mounted filesystem; mount the storage before installing').fix).toContain('Mount that storage');
  });

  it('sends an installed package to management instead of a retry', () => {
    const problem = explainPlanError(409, 'catalog: "x" is already installed; use configure');
    expect(problem.installed).toBe(true);
  });

  it('names an unreachable appliance', () => {
    expect(explainPlanError(0, null).fix).toContain('homestead-server');
  });

  it('always gives a fix, even for unknown errors', () => {
    const problem = explainPlanError(422, 'something new');
    expect(problem.fix).not.toBe('');
    expect(problem.installed).toBe(false);
  });
});

describe('plan summary', () => {
  it('lists what is created, changed and left alone in plain words', () => {
    const plan = {
      Package: 'app',
      Requirements: { Passed: true },
      Routes: [{ Host: 'app.lan', Exposure: 'local' }],
      Compose: { ComposeYAML: '' },
      TLS: [{ Host: 'app.lan', Issuer: 'internal' }],
      Firewall: [{ Service: { Name: 'web-https' } }],
      DNS: [],
      Directories: [{ Mountpoint: '/mnt/a', Directory: { Path: 'app/config' } }],
      EdgeNetwork: true,
      Actions: ['provision', 'up'],
    } as InstallPlan;
    const summary = planSummary(plan);
    expect(summary.created).toContain('Folder /mnt/a/app/config');
    expect(summary.created).toContain('The homestead-edge network, if it does not exist yet');
    expect(summary.created).toContain('Address app.lan (local network only)');
    expect(summary.created).toContain('Certificate for app.lan from your Homestead internal CA');
    expect(summary.changed).toEqual(['Firewall rule for web-https']);
    expect(summary.leftAlone.join(' ')).toContain('does not deploy or restart');
  });
});
