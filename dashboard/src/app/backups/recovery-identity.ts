import { Component, inject, OnInit, output, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import {
  ApiService,
  BackupIdentityExport,
  BackupIdentityRotate,
  BackupIdentityStatus,
} from '../core/api.service';
import { WhenPipe } from '../core/format-time';
import { labelForType } from '../core/operation-labels';
import { WorkOrders } from '../core/work-orders.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';

/**
 * The recovery identity: export it once after an explicit, audited
 * confirmation, then confirm it is stored offline. Shared by the Backups page
 * and the setup wizard's account step so both call the same routes. The key
 * is held in a signal only until the owner dismisses it; it is never written
 * to storage.
 */
@Component({
  imports: [StateMessage, Dialog, WhenPipe],
  selector: 'app-recovery-identity',
  templateUrl: './recovery-identity.html',
  styles: ['.backup-root { overflow-wrap: anywhere; }'],
})
export class RecoveryIdentity implements OnInit {
  private readonly api = inject(ApiService);
  private readonly orders = inject(WorkOrders);

  /** Emitted after an export or a confirmation, so a host can re-read what depends on it. */
  readonly changed = output<void>();

  protected readonly identity = signal<BackupIdentityStatus | null>(null);
  protected readonly confirmingExport = signal(false);
  /** The exported key, held only until the owner dismisses it. */
  protected readonly exported = signal<BackupIdentityExport | null>(null);
  protected readonly confirmingRotate = signal(false);
  /** What a rotation did to existing runs; shown with the new key, dropped with it. */
  protected readonly rotation = signal<BackupIdentityRotate | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.api.backupIdentity().subscribe({
      next: (status) => this.identity.set(status),
      error: () => this.identity.set(null),
    });
  }

  askExport(): void {
    this.confirmingExport.set(true);
  }

  cancelExport(): void {
    this.confirmingExport.set(false);
  }

  /** Explicit, audited export; the key is shown once, then dropped. */
  confirmExport(): void {
    this.confirmingExport.set(false);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.backupIdentityExport().subscribe({
      next: (result) => {
        this.busy.set(false);
        this.exported.set(result);
        this.load();
        this.changed.emit();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  askRotate(): void {
    this.confirmingRotate.set(true);
  }

  cancelRotate(): void {
    this.confirmingRotate.set(false);
  }

  /**
   * Replaces the recovery identity (audited, fresh sign-in). The new key is
   * shown once through the same reveal as an export; the owner then confirms
   * it is stored offline.
   */
  confirmRotate(): void {
    this.confirmingRotate.set(false);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.backupIdentityRotate().subscribe({
      next: (result) => {
        this.busy.set(false);
        this.rotation.set(result);
        this.exported.set(result);
        // The re-wrap runs in the background; the work-orders rail follows it.
        if (result.rewrap_operation_id) {
          this.orders.track(result.rewrap_operation_id, labelForType('backup.rewrap'));
        }
        this.load();
        this.changed.emit();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  /**
   * Re-wraps existing backups to the current key without minting one. The
   * request starts a background operation the rail follows.
   */
  rewrap(): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.backupIdentityRewrap().subscribe({
      next: () => {
        this.busy.set(false);
        this.notice.set('Re-wrapping existing backups in the background.');
        this.load();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  copyExport(): void {
    const key = this.exported()?.identity;
    if (key && navigator.clipboard) {
      void navigator.clipboard.writeText(key);
      this.notice.set('Copied. Store it offline, then dismiss it here.');
    }
  }

  downloadExport(): void {
    const result = this.exported();
    if (!result) {
      return;
    }
    const url = URL.createObjectURL(
      new Blob([result.identity + '\n'], { type: 'text/plain' }),
    );
    const link = document.createElement('a');
    link.href = url;
    link.download = 'homestead-backup-identity.txt';
    link.click();
    URL.revokeObjectURL(url);
  }

  /**
   * Dropping the signal is the only copy the page holds; the key is
   * never written to storage. Dismissing without acknowledging leaves
   * the doctor warning in place.
   */
  dismissExport(): void {
    this.exported.set(null);
    this.rotation.set(null);
    this.notice.set(null);
  }

  /** The owner confirms the key is stored offline; then it is dropped. */
  acknowledgeExport(): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.backupIdentityAck().subscribe({
      next: () => {
        this.busy.set(false);
        this.exported.set(null);
        this.rotation.set(null);
        this.notice.set('Recorded: the recovery identity is stored offline.');
        this.load();
        this.changed.emit();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  /** Exported earlier but never confirmed stored. */
  pendingAck(): boolean {
    const status = this.identity();
    return !!status && status.exported && !status.acknowledged;
  }
}
