import { Component, inject, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { JsonPipe } from '@angular/common';
import { ApiService, ImportPreview } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { StateMessage } from '../state/state-message';

/**
 * Configuration export and import (S483). Export downloads the portable
 * YAML, the same document `homesteadctl config export` writes. It holds
 * secret names, never values, so it needs no fresh sign-in. Import uploads
 * a file, shows the daemon's validation and a diff, takes any secret values
 * in password fields, and applies only after an explicit confirm. Secret
 * values live in the inputs and one request: no signal, storage or cache
 * holds them, and the fields are cleared after the call.
 */
@Component({
  imports: [StateMessage, JsonPipe],
  selector: 'app-config-transfer',
  templateUrl: './config-transfer.html',
})
export class ConfigTransfer {
  private readonly api = inject(ApiService);

  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly preview = signal<ImportPreview | null>(null);
  /** Bumped to rebuild the password fields empty. */
  protected readonly fieldsRevision = signal(0);
  /** Typed secret values by name; not reactive, never rendered, cleared after use. */
  private values = new Map<string, string>();

  protected exportYaml(): void {
    this.begin();
    this.api.configExportYaml().subscribe({
      next: (text) => {
        this.busy.set(false);
        const url = URL.createObjectURL(new Blob([text], { type: 'application/yaml' }));
        const link = document.createElement('a');
        link.href = url;
        link.download = 'homestead-config.yaml';
        link.click();
        setTimeout(() => URL.revokeObjectURL(url), 10000);
        this.notice.set('Exported homestead-config.yaml. It holds secret names, not values.');
      },
      error: (err: HttpErrorResponse) => this.fail(err, 'Exporting configuration'),
    });
  }

  protected async choose(event: Event): Promise<void> {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) {
      return;
    }
    this.begin();
    this.preview.set(null);
    const text = await file.text();
    input.value = '';
    this.api.previewConfigImport(text).subscribe({
      next: (preview) => {
        this.busy.set(false);
        this.discardSecrets();
        this.preview.set(preview);
      },
      error: (err: HttpErrorResponse) => this.fail(err, 'Checking the file'),
    });
  }

  protected typed(name: string, event: Event): void {
    this.values.set(name, (event.target as HTMLInputElement).value);
  }

  protected cancel(): void {
    this.discardSecrets();
    this.preview.set(null);
    this.notice.set(null);
    this.error.set(null);
  }

  protected confirm(): void {
    const preview = this.preview();
    if (!preview?.valid || !preview.document) {
      return;
    }
    const secrets: Record<string, string> = {};
    for (const name of preview.secrets) {
      secrets[name] = this.values.get(name) ?? '';
    }
    this.begin();
    this.api.importConfig(preview.document, secrets).subscribe({
      next: (result) => {
        this.busy.set(false);
        this.discardSecrets();
        this.preview.set(null);
        this.notice.set(
          `Imported ${result.settings} settings and ${result.secrets} secrets. Reload the page to see the new settings.`,
        );
      },
      error: (err: HttpErrorResponse) => {
        this.discardSecrets();
        this.fail(err, 'Importing configuration');
      },
    });
  }

  private begin(): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
  }

  private fail(err: HttpErrorResponse, doing: string): void {
    this.busy.set(false);
    this.error.set(httpErrorMessage(err, doing));
  }

  private discardSecrets(): void {
    this.values = new Map();
    this.fieldsRevision.update((n) => n + 1);
  }
}
