import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { KeyValuePipe } from '@angular/common';
import { RouterLink } from '@angular/router';
import {
  ApiService,
  PlatformRelease,
  PlatformUpdates,
  TrustKey,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';

/**
 * Platform release verification (S264, ADR-025): per-release
 * verification state, local artifact verification, and owner-managed
 * trusted keys. Nothing here fetches; the owner names files already on
 * the appliance. A rollback refusal offers an explicit, audited
 * override; authenticity failures offer none.
 */
@Component({
  imports: [StateMessage, RouterLink, KeyValuePipe],
  selector: 'app-platform-updates',
  templateUrl: './platform-updates.html',
})
export class PlatformUpdatesPanel implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly view = signal<PlatformUpdates | null>(null);
  protected readonly keys = signal<TrustKey[]>([]);
  protected readonly last = signal<PlatformRelease | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);

  protected component = 'homestead-server';
  protected artifact = '';
  protected checksums = '';
  protected signature = '';
  protected allowOlder = false;
  protected applyConfirmed = false;
  protected applyOlderConfirmed = false;
  protected readonly applyOperation = signal<number | null>(null);
  protected readonly olderRefused = signal(false);
  protected newKey = '';
  protected newLabel = '';

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.api.platformUpdates().subscribe({
      next: (view) => this.view.set(view),
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
    this.api.trustKeys().subscribe({
      next: (keys) => this.keys.set(keys),
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
  }

  verify(): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api
      .platformVerify({
        component: this.component,
        artifact: this.artifact,
        checksums: this.checksums,
        signature: this.signature,
        allow_older: this.allowOlder,
      })
      .subscribe({
        next: (release) => {
          this.busy.set(false);
          this.last.set(release);
          this.allowOlder = false;
          this.refresh();
        },
        error: (failure: HttpErrorResponse) => this.fail(failure),
      });
  }

  /** True when applying this verified release moves to an older one. */
  needsOlder(release: PlatformRelease): boolean {
    return release.override || !!release.plan_error || this.olderRefused();
  }

  /**
   * Applies a verified release (S265). The artifact path is the one
   * entered above: the daemon re-checks its bytes against the verified
   * digest. The daemon restarts to finish; progress is the operation.
   */
  apply(release: PlatformRelease): void {
    if (!this.applyConfirmed) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.applyOperation.set(null);
    this.api
      .platformApply({
        component: release.component,
        artifact: this.artifact,
        allow_older: this.applyOlderConfirmed,
      })
      .subscribe({
        next: (started) => {
          this.busy.set(false);
          this.applyOperation.set(started.id);
          this.applyConfirmed = false;
          this.applyOlderConfirmed = false;
          this.olderRefused.set(false);
          this.refresh();
        },
        error: (failure: HttpErrorResponse) => {
          if (String(failure.error ?? '').includes('older')) {
            this.olderRefused.set(true);
          }
          this.fail(failure);
        },
      });
  }

  addKey(): void {
    this.mutateKey(this.api.trustKeyAdd(this.newKey, this.newLabel), 'Key trusted.');
  }

  replaceKey(keyId: string): void {
    this.mutateKey(
      this.api.trustKeyReplace(keyId, this.newKey, this.newLabel),
      'Key replaced.',
    );
  }

  removeKey(keyId: string): void {
    this.mutateKey(this.api.trustKeyRemove(keyId), 'Key removed.');
  }

  private mutateKey(
    call: { subscribe: (observer: object) => unknown },
    message: string,
  ): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    call.subscribe({
      next: () => {
        this.busy.set(false);
        this.notice.set(message);
        this.newKey = '';
        this.newLabel = '';
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
  }

  private fail(failure: HttpErrorResponse): void {
    this.busy.set(false);
    this.error.set(failure.error ?? failure.message);
  }
}
