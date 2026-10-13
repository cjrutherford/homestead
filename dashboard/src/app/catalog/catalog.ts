import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import {
  ApiService,
  AppSecret,
  CatalogManifest,
  InstallPlan,
  PackageSummary,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';

interface VolumeRow {
  service: string;
  class: string;
  subpath: string;
  target: string;
  readonly: boolean;
}

interface NetworkRow {
  name: string;
  external: boolean;
}

interface ConfigRow {
  service: string;
  key: string;
  value: string;
}

/**
 * Catalog install/configure flow (S167): browse first-party packages,
 * review requirements, plan with owner inputs, install through the
 * operation engine, and configure services afterwards. Long executions
 * link out to operation detail for progress; this page never blocks.
 */
@Component({
  imports: [StateMessage, RouterLink],
  selector: 'app-catalog',
  templateUrl: './catalog.html',
})
export class Catalog implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly packages = signal<PackageSummary[]>([]);
  protected readonly selected = signal<CatalogManifest | null>(null);
  protected readonly plan = signal<InstallPlan | null>(null);
  protected readonly operationId = signal<number | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  protected readonly domain = signal('lan');
  protected readonly containerPorts = signal<Record<string, string>>({});
  protected readonly mountpoints = signal<Record<string, string>>({});
  protected readonly volumes = signal<VolumeRow[]>([]);
  protected readonly networks = signal<NetworkRow[]>([
    { name: 'homestead-edge', external: true },
  ]);
  protected readonly zone = signal('');
  protected readonly publicIp = signal('');
  protected readonly configRows = signal<ConfigRow[]>([]);
  protected readonly unsetRows = signal<ConfigRow[]>([]);
  protected readonly appSecrets = signal<AppSecret[]>([]);
  protected readonly secretValues = signal<Record<string, string>>({});
  protected readonly skipVerify = signal(false);
  protected readonly force = signal(false);
  protected readonly secretNotice = signal<string | null>(null);

  /** Installed packages cannot be installed again (S276). */
  protected isInstalled(): boolean {
    const name = this.selected()?.Name;
    return this.packages().some((pkg) => pkg.name === name && pkg.installed === true);
  }

  ngOnInit(): void {
    this.busy.set(true);
    this.api.catalogPackages().subscribe({
      next: (packages) => {
        this.busy.set(false);
        this.packages.set(packages);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.message);
      },
    });
  }

  select(name: string): void {
    this.busy.set(true);
    this.error.set(null);
    this.plan.set(null);
    this.operationId.set(null);
    this.api.catalogPackage(name).subscribe({
      next: (manifest) => {
        this.busy.set(false);
        this.selected.set(manifest);
        const ports: Record<string, string> = {};
        for (const service of manifest.Services) {
          ports[service.name] =
            manifest.Requirements.Ports.length > 0
              ? String(manifest.Requirements.Ports[0])
              : '';
        }
        this.containerPorts.set(ports);
        const mounts: Record<string, string> = {};
        for (const need of manifest.Requirements.Storage) {
          mounts[need.Class] = '';
        }
        this.mountpoints.set(mounts);
        this.volumes.set([]);
        this.configRows.set([{ service: manifest.Services[0]?.name ?? '', key: '', value: '' }]);
        this.unsetRows.set([]);
        this.appSecrets.set([]);
        this.secretValues.set({});
        this.secretNotice.set(null);
        if ((manifest.Secrets ?? []).length > 0) {
          this.api.appSecrets(manifest.Name).subscribe({
            next: (secrets) => this.appSecrets.set(secrets),
            error: (failure: HttpErrorResponse) => this.error.set(failure.message),
          });
        }
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.message);
      },
    });
  }

  private installBody(): Record<string, unknown> {
    const ports: Record<string, number> = {};
    for (const [service, text] of Object.entries(this.containerPorts())) {
      if (text.trim() !== '') {
        ports[service] = Number(text);
      }
    }
    const storage: Record<string, string> = {};
    for (const [className, mount] of Object.entries(this.mountpoints())) {
      if (mount.trim() !== '') {
        storage[className] = mount.trim();
      }
    }
    return {
      domain: this.domain().trim(),
      hostnames: {},
      container_ports: ports,
      storage,
      volumes: this.volumes().filter(
        (row) => row.service.trim() !== '' && row.target.trim() !== '',
      ),
      networks: this.networks().filter((row) => row.name.trim() !== ''),
      zone: this.zone().trim(),
      public_ip: this.publicIp().trim(),
    };
  }

  reviewPlan(): void {
    const manifest = this.selected();
    if (manifest === null) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.planInstall(manifest.Name, this.installBody()).subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.plan.set(plan);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  install(): void {
    const manifest = this.selected();
    if (manifest === null) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.installPackage(manifest.Name, this.installBody()).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.operationId.set(id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  addVolumeRow(): void {
    const manifest = this.selected();
    this.volumes.update((rows) => [
      ...rows,
      {
        service: manifest?.Services[0]?.name ?? '',
        class: manifest?.Requirements.Storage[0]?.Class ?? '',
        subpath: '',
        target: '',
        readonly: false,
      },
    ]);
  }

  // Template bindings cannot carry arrow functions, so row edits go
  // through these patch methods with plain object literals.
  setContainerPort(service: string, value: string): void {
    this.containerPorts.update((ports) => ({ ...ports, [service]: value }));
  }

  setMountpoint(className: string, value: string): void {
    this.mountpoints.update((mounts) => ({ ...mounts, [className]: value }));
  }

  patchVolume(index: number, patch: Partial<VolumeRow>): void {
    this.volumes.update((rows) =>
      rows.map((entry, position) =>
        position === index ? { ...entry, ...patch } : entry,
      ),
    );
  }

  patchNetwork(index: number, patch: Partial<NetworkRow>): void {
    this.networks.update((rows) =>
      rows.map((entry, position) =>
        position === index ? { ...entry, ...patch } : entry,
      ),
    );
  }

  patchConfigRow(index: number, patch: Partial<ConfigRow>): void {
    this.configRows.update((rows) =>
      rows.map((entry, position) =>
        position === index ? { ...entry, ...patch } : entry,
      ),
    );
  }

  patchUnsetRow(index: number, patch: Partial<ConfigRow>): void {
    this.unsetRows.update((rows) =>
      rows.map((entry, position) =>
        position === index ? { ...entry, ...patch } : entry,
      ),
    );
  }

  addNetworkRow(): void {
    this.networks.update((rows) => [...rows, { name: '', external: false }]);
  }

  addConfigRow(unset: boolean): void {
    const manifest = this.selected();
    const row = {
      service: manifest?.Services[0]?.name ?? '',
      key: '',
      value: '',
    };
    if (unset) {
      this.unsetRows.update((rows) => [...rows, row]);
    } else {
      this.configRows.update((rows) => [...rows, row]);
    }
  }

  applyConfig(): void {
    const manifest = this.selected();
    if (manifest === null) {
      return;
    }
    const byService = new Map<string, { Set: Record<string, string>; Unset: string[] }>();
    for (const row of this.configRows()) {
      if (row.service.trim() === '' || row.key.trim() === '') {
        continue;
      }
      const entry = byService.get(row.service) ?? { Set: {}, Unset: [] };
      entry.Set[row.key.trim()] = row.value;
      byService.set(row.service, entry);
    }
    for (const row of this.unsetRows()) {
      if (row.service.trim() === '' || row.key.trim() === '') {
        continue;
      }
      const entry = byService.get(row.service) ?? { Set: {}, Unset: [] };
      entry.Unset.push(row.key.trim());
      byService.set(row.service, entry);
    }
    const changes = [...byService.entries()].map(([Service, entry]) => ({
      Service,
      ...entry,
    }));
    this.busy.set(true);
    this.error.set(null);
    this.api.configurePackage(manifest.Name, changes).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.operationId.set(id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  patchSecretValue(name: string, value: string): void {
    this.secretValues.update((values) => ({ ...values, [name]: value }));
  }

  /** Registers the owner-supplied existing value (installs that predate S266). */
  registerSecret(secret: string): void {
    const manifest = this.selected();
    const value = this.secretValues()[secret] ?? '';
    if (manifest === null || value === '') {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.registerAppSecret(manifest.Name, secret, value, this.skipVerify(), this.force()).subscribe({
      next: () => {
        this.busy.set(false);
        this.secretValues.update((values) => ({ ...values, [secret]: '' }));
        this.secretNotice.set(`Registered ${secret}.`);
        this.api.appSecrets(manifest.Name).subscribe((secrets) => this.appSecrets.set(secrets));
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  /** Rotates a generated secret through the configure operation. */
  rotateSecret(secret: string): void {
    const manifest = this.selected();
    if (manifest === null) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.configurePackage(manifest.Name, [], [secret]).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.operationId.set(id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  /** True when an installed stack predates S266 and has no secret wiring. */
  protected canAdopt(): boolean {
    const secrets = this.appSecrets();
    return secrets.length > 0 && secrets[0].installed && !secrets[0].wired;
  }

  /** Regenerates only the secret wiring; every secret must be registered first. */
  adoptSecrets(): void {
    const manifest = this.selected();
    if (manifest === null) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.adoptSecrets(manifest.Name).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.operationId.set(id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }
}
