import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import {
  ApiService,
  DataMigrationPlan,
  DataMigrations,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';

/**
 * App data placement and migration (S277): where each installed app's
 * data lives, the reviewed migration plan (targets, sizes, free space,
 * downtime, backup verdict), approval of exactly that plan, and the
 * separate, explicit removal of old Docker volumes. Nothing runs from
 * this page without an approve click quoting the plan's digest.
 */
@Component({
  imports: [OperationProgress, Loading, StateMessage, Dialog],
  selector: 'app-data-migration',
  templateUrl: './data-migration.html',
})
export class DataMigration implements OnInit {
  private readonly api = inject(ApiService);

  protected stateLabel(state: string): string {
    switch (state) {
      case 'placed':
        return 'On mounted storage';
      case 'needs-migration':
        return 'Needs migrating';
      case 'unknown':
        return 'Placement unclear';
      default:
        return state;
    }
  }

  protected stateBadge(state: string): string {
    switch (state) {
      case 'placed':
        return 'badge-ok';
      case 'needs-migration':
      case 'unknown':
        return 'badge-warn';
      default:
        return '';
    }
  }

  protected readonly data = signal<DataMigrations>({ apps: [], old_volumes: [] });
  protected readonly plan = signal<DataMigrationPlan | null>(null);
  protected readonly planFor = signal<string | null>(null);
  protected readonly acknowledged = signal(false);
  protected readonly operationId = signal<number | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.dataMigrations().subscribe({
      next: (data) => {
        this.busy.set(false);
        this.data.set({ apps: data.apps ?? [], old_volumes: data.old_volumes ?? [] });
      },
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
  }

  pendingVolumes(name: string) {
    return this.data().old_volumes.filter(
      (volume) => volume.package === name && volume.state === 'pending-removal',
    );
  }

  reviewPlan(name: string): void {
    this.busy.set(true);
    this.error.set(null);
    this.operationId.set(null);
    this.acknowledged.set(false);
    this.api.planDataMigration(name).subscribe({
      next: (plan) => {
        this.busy.set(false);
        this.plan.set(plan);
        this.planFor.set(name);
      },
      error: (failure: HttpErrorResponse) => {
        this.plan.set(null);
        this.planFor.set(null);
        this.fail(failure);
      },
    });
  }

  setAcknowledged(value: boolean): void {
    this.acknowledged.set(value);
  }

  approve(): void {
    const plan = this.plan();
    const name = this.planFor();
    if (!plan || !name) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api
      .approveDataMigration(name, {
        digest: plan.digest,
        acknowledge_no_backup: this.acknowledged(),
      })
      .subscribe({
        next: ({ id }) => {
          this.busy.set(false);
          this.operationId.set(id);
          this.plan.set(null);
          this.planFor.set(null);
        },
        error: (failure: HttpErrorResponse) => this.fail(failure),
      });
  }

  cancelPlan(): void {
    this.plan.set(null);
    this.planFor.set(null);
  }

  protected readonly confirmingVolumes = signal<string | null>(null);

  askRemoveVolumes(name: string): void {
    this.confirmingVolumes.set(name);
  }

  cancelRemoveVolumes(): void {
    this.confirmingVolumes.set(null);
  }

  confirmRemoveVolumes(): void {
    const name = this.confirmingVolumes();
    this.confirmingVolumes.set(null);
    if (name) this.removeVolumes(name);
  }

  removeVolumes(name: string): void {
    const volumes = this.pendingVolumes(name).map((volume) => volume.volume);
    if (volumes.length === 0) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.removeOldVolumes(name, volumes).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.operationId.set(id);
      },
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
  }

  private fail(failure: HttpErrorResponse): void {
    this.busy.set(false);
    this.error.set(failure.error ?? failure.message);
  }
}
