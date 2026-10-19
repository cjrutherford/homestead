import { Component, DestroyRef, computed, inject, input, output, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { forkJoin } from 'rxjs';
import { ApiService, CatalogManifest, OperationState } from '../core/api.service';
import { isTerminal } from '../core/operation-view';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { OperationProgress } from '../ui/operation-progress';
import { removeAppRoutes } from './app-routes';
import { storageClasses } from './wizard-model';

/** One folder that "also delete its data" would remove, named in full. */
export interface DataRoot {
  mountpoint: string;
  path: string;
}

/**
 * Uninstall an app (S285), the dashboard side of `homesteadctl apps uninstall`.
 * The button opens a danger dialog that says in plain words what is removed
 * and what is kept. Data is kept unless the owner ticks "Also delete its
 * data", and then every folder is listed by its full path (the route takes
 * explicit roots, never guesses). The operation joins the work-orders rail
 * through the interceptor; progress also shows here.
 */
@Component({
  selector: 'app-uninstall-app',
  imports: [Dialog, OperationProgress, StateMessage, RouterLink],
  template: `
    @if (operationId() === null) {
      <button type="button" class="danger quiet" [attr.aria-busy]="loading() ? 'true' : null" [disabled]="loading()" (click)="open()">
        Uninstall {{ title() }}
      </button>
    }
    @if (loadError()) {
      <app-state-message kind="error" [headline]="loadError()" detail="Check that the appliance is running, then try again." />
    }
    @if (confirming()) {
      <app-dialog [heading]="'Uninstall ' + title() + '?'" variant="danger" (dismissed)="cancel()">
        <p>
          This stops {{ title() }} and removes its containers, its network and the files Homestead
          generated for it.
        </p>
        @if (stateless()) {
          <p>{{ title() }} keeps nothing on the appliance, so there is no data to keep or delete.</p>
        } @else if (roots().length === 0) {
          <p>
            Its data stays where it is. Homestead cannot tell which folders hold it, so delete them
            yourself if you no longer want them.
          </p>
        } @else {
          <p>Its data is kept unless you choose to delete it. These folders hold it:</p>
          <ul>
            @for (root of roots(); track root.mountpoint + root.path) {
              <li>
                <code>{{ root.mountpoint }}/{{ root.path }}</code>
              </li>
            }
          </ul>
          <label>
            <input type="checkbox" [checked]="removeData()" (change)="removeData.set($any($event.target).checked)" />
            Also delete its data
          </label>
          @if (removeData()) {
            <p class="warning">
              This permanently deletes everything in the folders above. It cannot be undone. Folders
              you mounted from elsewhere are not touched.
            </p>
          }
        }
        @if (hasSecrets()) {
          <p>
            The passwords Homestead created stay in the secret store, so installing again picks up
            where this left off.
          </p>
        }
        @if (refusal(); as refused) {
          <app-state-message kind="error" [headline]="refused" detail="Nothing was removed." />
          @if (canAbandon()) {
            <p>
              <a routerLink="/data">Plan a data move</a> to bring that data onto mounted storage first.
            </p>
            <label>
              <input type="checkbox" [checked]="abandon()" (change)="abandon.set($any($event.target).checked)" />
              Uninstall anyway and leave that data behind in Docker volumes
            </label>
          }
        }
        <div class="dialog-actions">
          <button type="button" (click)="cancel()">Keep {{ title() }}</button>
          <button type="button" class="danger" [attr.aria-busy]="busy() ? 'true' : null" [disabled]="busy()" (click)="confirm()">
            Confirm uninstall
          </button>
        </div>
      </app-dialog>
    }
    @if (operationId(); as id) {
      <app-operation-progress [id]="id" [operation]="operation()" />
      @if (operation()?.status === 'succeeded') {
        <app-state-message
          kind="status"
          [headline]="title() + ' uninstalled'"
          [detail]="'Its containers and generated files are gone. Its data was ' + (dataDeleted() ? 'deleted.' : 'kept.')"
          actionLabel="Back to the catalog"
          actionLink="/catalog"
        />
        @if (routeNote(); as note) {
          <p class="hint" role="status">{{ note }}</p>
        }
        @if (routeProblem(); as problem) {
          <p class="field-error" role="alert">
            {{ problem }} <a routerLink="/edge">Open the Edge page</a> to remove it by hand.
          </p>
        }
      }
    }
  `,
})
export class UninstallApp {
  private readonly api = inject(ApiService);
  private readonly destroyRef = inject(DestroyRef);

  readonly name = input.required<string>();
  readonly title = input.required<string>();
  /** Emitted when the app is gone, so a list can refresh. */
  readonly uninstalled = output<void>();

  protected readonly loading = signal(false);
  protected readonly loadError = signal<string | null>(null);
  protected readonly confirming = signal(false);
  protected readonly busy = signal(false);
  protected readonly manifest = signal<CatalogManifest | null>(null);
  protected readonly assignments = signal<Record<string, string>>({});
  protected readonly removeData = signal(false);
  protected readonly abandon = signal(false);
  protected readonly refusal = signal<string | null>(null);
  protected readonly canAbandon = signal(false);
  protected readonly operationId = signal<number | null>(null);
  protected readonly operation = signal<OperationState | null>(null);
  /** What the owner chose when they confirmed, for the result line. */
  protected readonly dataDeleted = signal(false);
  /** What happened to the app's address in the edge route table. */
  protected readonly routeNote = signal<string | null>(null);
  protected readonly routeProblem = signal<string | null>(null);

  protected readonly stateless = computed(() => {
    const manifest = this.manifest();
    return manifest !== null && storageClasses(manifest).length === 0;
  });
  protected readonly hasSecrets = computed(() => (this.manifest()?.Secrets ?? []).length > 0);
  /** The folders data removal would name: <mount>/<package> for each class the app keeps data on. */
  protected readonly roots = computed<DataRoot[]>(() => {
    const manifest = this.manifest();
    if (manifest === null) {
      return [];
    }
    const found: DataRoot[] = [];
    for (const className of storageClasses(manifest)) {
      const mountpoint = this.assignments()[className];
      if (!mountpoint) {
        return [];
      }
      found.push({ mountpoint, path: manifest.Name });
    }
    return found;
  });

  protected open(): void {
    this.loading.set(true);
    this.loadError.set(null);
    forkJoin([this.api.catalogPackage(this.name()), this.api.storageAssignments()])
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ([manifest, assigned]) => {
          this.manifest.set(manifest);
          const byClass: Record<string, string> = {};
          for (const entry of assigned) {
            byClass[entry.Class] = entry.Mountpoint;
          }
          this.assignments.set(byClass);
          this.removeData.set(false);
          this.abandon.set(false);
          this.refusal.set(null);
          this.canAbandon.set(false);
          this.loading.set(false);
          this.confirming.set(true);
        },
        error: (failure: HttpErrorResponse) => {
          this.loading.set(false);
          this.loadError.set(
            failure.status === 0
              ? `Cannot reach the appliance, so ${this.title()} was not uninstalled.`
              : `Could not read what ${this.title()} keeps: ${typeof failure.error === 'string' && failure.error ? failure.error : failure.message}`,
          );
        },
      });
  }

  protected cancel(): void {
    this.confirming.set(false);
  }

  protected confirm(): void {
    const body: { remove_data?: boolean; abandon_volumes?: boolean; data_dirs?: { mountpoint: string; path: string }[] } = {};
    if (this.removeData() && this.roots().length > 0) {
      body.remove_data = true;
      body.data_dirs = this.roots();
    }
    if (this.abandon()) {
      body.abandon_volumes = true;
    }
    this.busy.set(true);
    this.refusal.set(null);
    this.dataDeleted.set(body.remove_data === true);
    this.api.uninstallPackage(this.name(), body).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.confirming.set(false);
        this.operationId.set(id);
        this.follow(id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        const text = typeof failure.error === 'string' ? failure.error.trim() : '';
        // 409 means data still sits in Docker volumes; 502 means the appliance could not tell.
        this.canAbandon.set(failure.status === 409 || failure.status === 502);
        this.refusal.set(
          failure.status === 0
            ? `Cannot reach the appliance, so ${this.title()} was not uninstalled.`
            : text || `${this.title()} was not uninstalled (${failure.status}).`,
        );
      },
    });
  }

  private routesHandled = false;

  /**
   * Takes the app's address out of the edge route table through the edge
   * routes API. The rest of the table is sent back unchanged. A table with no
   * route for the app, or an edge that refuses, never fails the uninstall.
   */
  private removeRoutes(): void {
    const manifest = this.manifest();
    if (manifest === null) {
      return;
    }
    removeAppRoutes(this.api, manifest).subscribe({
      next: (removed) => {
        if (removed === null) {
          return;
        }
        const where = removed.hosts.join(', ');
        if (removed.operation === null) {
          this.routeProblem.set(`Its address ${where} is still in the edge route table.`);
          return;
        }
        this.api
          .operationEvents(removed.operation.id)
          .pipe(takeUntilDestroyed(this.destroyRef))
          .subscribe((state) => {
            if (isTerminal(state.status)) {
              if (state.status === 'succeeded') {
                this.routeNote.set(`Its address ${where} was removed from the edge.`);
              } else {
                this.routeProblem.set(`Its address ${where} is still in the edge route table.`);
              }
            }
          });
      },
      error: () => undefined,
    });
  }

  private follow(id: number): void {
    this.api
      .operationEvents(id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (state) => {
          this.operation.set(state);
          if (isTerminal(state.status) && state.status === 'succeeded') {
            if (!this.routesHandled) {
              this.routesHandled = true;
              this.removeRoutes();
            }
            this.uninstalled.emit();
          }
        },
        error: () => undefined,
      });
  }
}
