import { Component, inject, OnInit, output, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ApiService, BackupIdentityExport, BackupIdentityStatus } from '../core/api.service';
import { WhenPipe } from '../core/format-time';
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

  /** Emitted after an export or a confirmation, so a host can re-read what depends on it. */
  readonly changed = output<void>();

  protected readonly identity = signal<BackupIdentityStatus | null>(null);
  protected readonly confirmingExport = signal(false);
  /** The exported key, held only until the owner dismisses it. */
  protected readonly exported = signal<BackupIdentityExport | null>(null);
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
