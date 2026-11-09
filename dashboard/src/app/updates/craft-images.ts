import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import {
  ApiService,
  CraftPlanResult,
  CraftStatus,
  TrustKey,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { OperationProgress } from '../ui/operation-progress';

/** Stable daemon error code: refused only for lack of the older-release override. */
const OLDER_RELEASE_CODE = 'older-release-needs-override';

/**
 * Craft images (S279, ADR-028): locally built images with their recorded
 * digests, the verify-only plan, the verify-then-build action, and the
 * craft signing keys (a separate class from the platform release keys).
 * Signature, artifact-digest and build-context-digest refusals offer no
 * override; only an older counter does, behind its own confirmation.
 */
@Component({
  imports: [OperationProgress, StateMessage],
  selector: 'app-craft-images',
  templateUrl: './craft-images.html',
})
export class CraftImagesPanel implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly status = signal<CraftStatus | null>(null);
  protected readonly keys = signal<TrustKey[]>([]);
  protected readonly result = signal<CraftPlanResult | null>(null);
  protected readonly operation = signal<number | null>(null);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly olderRefused = signal(false);

  protected app = '';
  protected version = '';
  protected manifest = '';
  protected signature = '';
  protected artifact = '';
  protected allowOlder = false;
  protected newKey = '';
  protected newLabel = '';

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.api.craftStatus().subscribe({
      next: (status) => this.status.set(status),
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
    this.api.craftTrustKeys().subscribe({
      next: (keys) => this.keys.set(keys),
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
  }

  private request() {
    return {
      app: this.app,
      version: this.version || undefined,
      manifest: this.manifest || undefined,
      signature: this.signature || undefined,
      artifact: this.artifact || undefined,
      allow_older: this.allowOlder,
    };
  }

  /** Verifies the manifest without building (signature, counter, artifact, context, disk). */
  plan(): void {
    this.begin();
    this.api.craftPlan(this.request()).subscribe({
      next: (result) => {
        this.busy.set(false);
        this.result.set(result);
        this.olderRefused.set(result.verdict.refusal === 'counter');
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => this.fail(failure),
    });
  }

  /** Starts the verify-then-build operation; everything is verified again. */
  build(): void {
    this.begin();
    this.operation.set(null);
    this.api.craftBuild(this.request()).subscribe({
      next: (started) => {
        this.busy.set(false);
        this.operation.set(started.id);
        this.allowOlder = false;
        this.olderRefused.set(false);
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        if (failure.headers?.get('X-Homestead-Error') === OLDER_RELEASE_CODE) {
          this.olderRefused.set(true);
        }
        this.fail(failure);
      },
    });
  }

  addKey(): void {
    this.mutateKey(this.api.craftTrustKeyAdd(this.newKey, this.newLabel), 'Craft key trusted.');
  }

  replaceKey(keyId: string): void {
    this.mutateKey(
      this.api.craftTrustKeyReplace(keyId, this.newKey, this.newLabel),
      'Craft key replaced.',
    );
  }

  removeKey(keyId: string): void {
    this.mutateKey(this.api.craftTrustKeyRemove(keyId), 'Craft key removed.');
  }

  private mutateKey(
    call: { subscribe: (observer: object) => unknown },
    message: string,
  ): void {
    this.begin();
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

  private begin(): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
  }

  private fail(failure: HttpErrorResponse): void {
    this.busy.set(false);
    this.error.set(failure.error ?? failure.message);
  }
}
