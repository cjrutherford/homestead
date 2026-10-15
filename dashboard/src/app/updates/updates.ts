import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, PackageUpdate } from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { CraftImagesPanel } from './craft-images';
import { PlatformUpdatesPanel } from './platform-updates';

/**
 * Update review page (S193): per-package drift plans against running
 * containers, explicit backup opt-in with a database password that
 * flows through once and is cleared after apply, and approved
 * application through the safe-update operation. Long executions link
 * out to operation detail for progress; this page never blocks.
 */
@Component({
  imports: [StateMessage, RouterLink, PlatformUpdatesPanel, CraftImagesPanel],
  selector: 'app-updates',
  templateUrl: './updates.html',
})
export class Updates implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly rows = signal<PackageUpdate[]>([]);
  protected readonly operationId = signal<number | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  protected readonly withBackup = signal<Record<string, boolean>>({});
  protected readonly dbPasswords = signal<Record<string, string>>({});
  /** Per-package craft inputs for updates that rebuild a craft image (S279). */
  protected readonly craftInputs = signal<
    Record<string, { manifest?: string; artifact?: string; allow_older?: boolean }>
  >({});

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.updates().subscribe({
      next: (rows) => {
        this.busy.set(false);
        this.rows.set(rows);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  setBackup(name: string, wanted: boolean): void {
    this.withBackup.update((flags) => ({ ...flags, [name]: wanted }));
  }

  setDbPassword(name: string, value: string): void {
    this.dbPasswords.update((passwords) => ({ ...passwords, [name]: value }));
  }

  setCraft(name: string, field: 'manifest' | 'artifact', value: string): void {
    this.craftInputs.update((all) => ({
      ...all,
      [name]: { ...all[name], [field]: value || undefined },
    }));
  }

  setCraftOlder(name: string, wanted: boolean): void {
    this.craftInputs.update((all) => ({
      ...all,
      [name]: { ...all[name], allow_older: wanted },
    }));
  }

  apply(name: string): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api
      .applyUpdate(name, {
        backup: this.withBackup()[name] ?? false,
        db_password: this.dbPasswords()[name] ?? '',
        ...(this.craftInputs()[name] ? { craft: this.craftInputs()[name] } : {}),
      })
      .subscribe({
        next: ({ id, detail }) => {
          this.busy.set(false);
          // A cleared password never lingers past the single call.
          this.dbPasswords.update((passwords) => ({ ...passwords, [name]: '' }));
          if (id === 0) {
            this.notice.set(detail ?? 'Already current.');
            this.refresh();
            return;
          }
          this.operationId.set(id);
        },
        error: (failure: HttpErrorResponse) => {
          this.busy.set(false);
          this.error.set(failure.error ?? failure.message);
        },
      });
  }
}
