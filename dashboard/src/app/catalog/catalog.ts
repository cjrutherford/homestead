import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import {
  ApiService,
  CatalogManifest,
  InstallPlan,
  PackageSummary,
} from '../core/api.service';

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
  imports: [RouterLink],
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
}
