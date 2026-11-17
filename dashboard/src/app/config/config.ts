import { Component, OnInit, inject, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { JsonPipe } from '@angular/common';
import { forkJoin } from 'rxjs';
import { ApiService } from '../core/api.service';
import { ConfigField, FieldError, FieldValues } from '../schema/schema';
import { SchemaForm } from '../schema/schema-form';
import { httpErrorMessage } from '../core/http-error';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import { ConfigTransfer } from './config-transfer';

export interface Change {
  key: string;
  before: unknown;
  after: unknown;
}

/**
 * Configuration page (S035): schema-rendered editor with review diff,
 * apply, and revert. Submit plans first — valid candidates show a diff for
 * confirmation, invalid ones surface per-field errors. Revert reloads the
 * saved state; apply persists and refreshes it.
 */
@Component({
  imports: [Loading, StateMessage, SchemaForm, JsonPipe, ConfigTransfer],
  selector: 'app-config',
  templateUrl: './config.html',
})
export class Config implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly loading = signal(true);
  protected readonly loadError = signal<string | null>(null);
  protected readonly fields = signal<ConfigField[]>([]);
  protected readonly values = signal<FieldValues>({});
  protected readonly errors = signal<FieldError[]>([]);
  protected readonly notice = signal<string | null>(null);
  protected readonly reviewing = signal(false);
  protected readonly candidate = signal<FieldValues>({});
  protected readonly changes = signal<Change[]>([]);
  protected readonly busy = signal(false);
  protected readonly revision = signal(0);

  ngOnInit(): void {
    forkJoin({ schema: this.api.configSchema(), config: this.api.config() }).subscribe({
      next: ({ schema, config }) => {
        this.fields.set(schema.fields);
        this.values.set({ ...config.settings });
        this.loading.set(false);
      },
      error: (err: HttpErrorResponse) => {
        this.loadError.set(httpErrorMessage(err, 'Loading configuration'));
        this.loading.set(false);
      },
    });
  }

  protected review(submitted: FieldValues): void {
    this.busy.set(true);
    this.notice.set(null);
    this.api.planConfig(submitted).subscribe({
      next: (plan) => {
        this.busy.set(false);
        if (!plan.valid) {
          this.errors.set(plan.errors ?? []);
          return;
        }
        this.candidate.set(plan.effective ?? submitted);
        this.changes.set(diff(this.values(), plan.effective ?? submitted));
        this.errors.set([]);
        this.reviewing.set(true);
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        this.errors.set([]);
        this.loadError.set(httpErrorMessage(err, 'Reviewing configuration'));
      },
    });
  }

  protected back(): void {
    this.reviewing.set(false);
  }

  protected confirm(): void {
    this.busy.set(true);
    this.api.applyConfig(this.candidate()).subscribe({
      next: (state) => {
        this.busy.set(false);
        this.values.set({ ...state.settings });
        this.candidate.set({});
        this.changes.set([]);
        this.reviewing.set(false);
        this.notice.set('Configuration saved.');
        this.revision.update((revision) => revision + 1);
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        this.reviewing.set(false);
        this.errors.set(readFieldErrors(err));
      },
    });
  }

  protected revert(): void {
    this.busy.set(true);
    this.notice.set(null);
    this.errors.set([]);
    this.api.config().subscribe({
      next: (config) => {
        this.busy.set(false);
        this.values.set({ ...config.settings });
        this.revision.update((revision) => revision + 1);
        this.notice.set('Reverted to saved configuration.');
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        this.loadError.set(httpErrorMessage(err, 'Reverting configuration'));
      },
    });
  }
}

function diff(before: FieldValues, after: FieldValues): Change[] {
  const changes: Change[] = [];
  for (const key of Object.keys(after)) {
    if (!(key in before)) {
      changes.push({ key, before: undefined, after: after[key] });
    } else if (!same(before[key], after[key])) {
      changes.push({ key, before: before[key], after: after[key] });
    }
  }
  for (const key of Object.keys(before)) {
    if (!(key in after)) {
      changes.push({ key, before: before[key], after: undefined });
    }
  }
  changes.sort((a, b) => (a.key < b.key ? -1 : 1));
  return changes;
}

function same(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

function readFieldErrors(err: HttpErrorResponse): FieldError[] {
  const body = err.error as { errors?: FieldError[] } | null;
  return body?.errors ?? [];
}
