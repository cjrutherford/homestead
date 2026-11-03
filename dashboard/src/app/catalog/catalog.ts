import { Component, computed, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { forkJoin } from 'rxjs';
import {
  ApiService,
  AppSecret,
  BundleReport,
  CatalogManifest,
  PackageSummary,
  RevealedSecret,
  WorksWith,
} from '../core/api.service';
import { registerPackageTitles } from '../core/operation-labels';
import { StateMessage } from '../state/state-message';
import { AppIcon } from '../ui/app-icon';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';
import { appRoutes } from './app-routes';
import { AppPublic } from './app-public';
import { ReadLinks } from './read-links';
import { AppMove } from './app-move';
import { AppSignIn } from './app-sign-in';
import { AppSetup } from './app-setup';
import { InstallDrafts } from './install-drafts';
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
  imports: [OperationProgress, Loading, StateMessage, RouterLink, UninstallApp, AppSignIn, AppPublic, ReadLinks, AppMove, AppSetup, Dialog, AppIcon],
  selector: 'app-catalog',
  templateUrl: './catalog.html',
})
export class Catalog implements OnInit {
  private readonly api = inject(ApiService);
  private readonly drafts = inject(InstallDrafts);

  /** The owner started an install here and has not finished with its wizard: the wizard picks it back up. */
  protected settingUp(name: string): boolean {
    return this.drafts.get(name)?.installOperation != null;
  }

  protected readonly packages = signal<PackageSummary[]>([]);
  protected readonly bundles = signal<BundleReport[]>([]);
  protected readonly selected = signal<CatalogManifest | null>(null);
  /** Counts finished moves, so the Public address section reads the new route. */
  protected readonly moves = signal(0);
  /** The masthead sentence: how many apps are installed, from the list the page reads. */
  protected readonly fact = computed(() => {
    const all = this.packages();
    if (!all.length) {
      return 'Apps you can install on this appliance.';
    }
    const installed = all.filter((p) => p.installed);
    const lead =
      installed.length === 0
        ? 'No apps are installed yet.'
        : installed.length === 1
          ? `${installed[0].title} is installed.`
          : `${installed.length} apps are installed.`;
    const left = all.length - installed.length;
    return left ? `${lead} ${left === 1 ? '1 more is' : `${left} more are`} ready to install.` : lead;
  });
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
  /** The secret whose reveal waits for the owner's confirmation (S312). */
  protected readonly confirmingReveal = signal<string | null>(null);
  /** A credential the owner chose to read; cleared when hidden or leaving the app. */
  protected readonly revealed = signal<RevealedSecret | null>(null);
  /** The managed app's local names (S295); empty until read or when it has none. */
  protected readonly localNames = signal<string[]>([]);

  /**
   * Groups resolved integrations by level in ADR-051 order (S341): the
   * panel reads from co-installation inward, never in manifest order.
   */
  protected worksByLevel(works: WorksWith[] | undefined): { level: string; works: WorksWith[] }[] {
    const groups: { level: string; works: WorksWith[] }[] = [];
    for (const level of ['L1', 'L2', 'L3', 'L4']) {
      const members = (works ?? []).filter((work) => work.Level === level);
      if (members.length > 0) {
        groups.push({ level, works: members });
      }
    }
    return groups;
  }

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
    // Bundle capacity is advisory: a grading failure must not block the
    // catalog list, so it stays silent here and the section stays empty.
    this.api.catalogBundles().subscribe({
      next: (bundles) => this.bundles.set(bundles),
      error: () => this.bundles.set([]),
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
        this.revealed.set(null);
        this.confirmingReveal.set(null);
        this.secretNotice.set(null);
        this.localNames.set([]);
        // Names are a convenience here: a failed read leaves them out.
        forkJoin([this.api.edgeRoutes(), this.api.names()]).subscribe({
          next: ([table, names]) => {
            const hosts = new Set(appRoutes(manifest, table.routes ?? []).map((route) => route.Host));
            this.localNames.set(names.apps.filter((app) => hosts.has(app.host)).flatMap((app) => app.names));
          },
          error: () => this.localNames.set([]),
        });
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

  protected onMoved(name: string): void {
    this.moves.update((n) => n + 1);
    this.select(name);
  }

  /** Opens a card's management panel, or closes it when it is open. */
  protected toggle(name: string): void {
    if (this.selected()?.Name === name) {
      this.close();
      return;
    }
    this.select(name);
  }

  protected close(): void {
    this.selected.set(null);
    this.operationId.set(null);
  }

  /** S324: the setup the card follows stopped, finished or was skipped; a finished one leaves the card. */
  protected onSetupSettled(name: string, status: string): void {
    const setup = status === 'stopped' ? ('stopped' as const) : undefined;
    this.packages.update((all) => all.map((p) => (p.name === name ? { ...p, setup } : p)));
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

  /** Asks before showing a credential meant for the owner (S312). */
  askReveal(secret: string): void {
    this.revealed.set(null);
    this.confirmingReveal.set(secret);
  }

  cancelReveal(): void {
    this.confirmingReveal.set(null);
  }

  /** Reads the credential; the appliance audits it and may ask for the password again. */
  confirmReveal(): void {
    const manifest = this.selected();
    const secret = this.confirmingReveal();
    if (manifest === null || secret === null) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.revealAppSecret(manifest.Name, secret).subscribe({
      next: (shown) => {
        this.busy.set(false);
        this.confirmingReveal.set(null);
        this.revealed.set(shown);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.confirmingReveal.set(null);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  hideRevealed(): void {
    this.revealed.set(null);
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
