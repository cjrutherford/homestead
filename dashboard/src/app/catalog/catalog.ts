import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import {
  ApiService,
  AppSecret,
  CatalogManifest,
  PackageSummary,
} from '../core/api.service';
import { registerPackageTitles } from '../core/operation-labels';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';
import { UninstallApp } from './uninstall-app';

interface ConfigRow {
  service: string;
  key: string;
  value: string;
}

/**
 * The catalog (S167, S285): browse first-party packages. A package that is
 * not installed starts the install wizard; one that is installed can be
 * managed here (variables and secrets) or uninstalled. Installing itself
 * lives in the wizard (`install-wizard.ts`), not on this page.
 */
@Component({
  imports: [OperationProgress, Loading, StateMessage, RouterLink, UninstallApp],
  selector: 'app-catalog',
  templateUrl: './catalog.html',
})
export class Catalog implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly packages = signal<PackageSummary[]>([]);
  protected readonly selected = signal<CatalogManifest | null>(null);
  protected readonly operationId = signal<number | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  protected readonly configRows = signal<ConfigRow[]>([]);
  protected readonly unsetRows = signal<ConfigRow[]>([]);
  protected readonly appSecrets = signal<AppSecret[]>([]);
  protected readonly secretValues = signal<Record<string, string>>({});
  protected readonly skipVerify = signal(false);
  protected readonly force = signal(false);
  protected readonly secretNotice = signal<string | null>(null);

  ngOnInit(): void {
    this.reload();
  }

  protected reload(): void {
    this.busy.set(true);
    this.api.catalogPackages().subscribe({
      next: (packages) => {
        this.busy.set(false);
        registerPackageTitles(packages);
        this.packages.set(packages);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(
          failure.status === 0
            ? 'Cannot reach the appliance. Check that homestead-server is running, then reload.'
            : failure.message,
        );
      },
    });
  }

  /** Opens the management panel for an installed package. */
  select(name: string): void {
    this.busy.set(true);
    this.error.set(null);
    this.operationId.set(null);
    this.api.catalogPackage(name).subscribe({
      next: (manifest) => {
        this.busy.set(false);
        this.selected.set(manifest);
        this.configRows.set([{ service: manifest.Services[0]?.Name ?? '', key: '', value: '' }]);
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

  protected close(): void {
    this.selected.set(null);
    this.operationId.set(null);
  }

  protected onUninstalled(name: string): void {
    if (this.selected()?.Name === name) {
      this.selected.set(null);
    }
    this.reload();
  }

  // Template bindings cannot carry arrow functions, so row edits go
  // through these patch methods with plain object literals.
  patchConfigRow(index: number, patch: Partial<ConfigRow>): void {
    this.configRows.update((rows) =>
      rows.map((entry, position) => (position === index ? { ...entry, ...patch } : entry)),
    );
  }

  patchUnsetRow(index: number, patch: Partial<ConfigRow>): void {
    this.unsetRows.update((rows) =>
      rows.map((entry, position) => (position === index ? { ...entry, ...patch } : entry)),
    );
  }

  addConfigRow(unset: boolean): void {
    const manifest = this.selected();
    const row = {
      service: manifest?.Services[0]?.Name ?? '',
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
