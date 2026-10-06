import { Component, computed, input, output, signal } from '@angular/core';
import { ConfigField, FieldError, FieldValues } from './schema';

/**
 * Schema-driven configuration form (S034): renders simple controls from
 * server field metadata — text (password-masked when sensitive), number
 * with bounds, checkbox, and select — grouped by `group`, advanced fields
 * behind a toggle, server 422 errors under their fields. Emits coerced
 * values on submit; the server remains the validation source of truth.
 */
@Component({
  selector: 'app-schema-form',
  templateUrl: './schema-form.html',
})
export class SchemaForm {
  readonly fields = input.required<ConfigField[]>();
  readonly values = input<FieldValues>({});
  readonly errors = input<FieldError[]>([]);
  readonly submitted = output<FieldValues>();

  protected readonly showAdvanced = signal(false);

  protected readonly visibleGroups = computed(() => {
    const groups = new Map<string, ConfigField[]>();
    for (const field of this.fields()) {
      if (field.advanced && !this.showAdvanced()) {
        continue;
      }
      const group = field.group ?? '';
      if (!groups.has(group)) {
        groups.set(group, []);
      }
      groups.get(group)?.push(field);
    }
    return [...groups.entries()];
  });

  protected errorFor(name: string): string | null {
    return this.errors().find((error) => error.field === name)?.reason ?? null;
  }

  protected current(field: ConfigField): unknown {
    const value = this.values()[field.name];
    return value === undefined ? (field.default ?? '') : value;
  }

  protected submit(form: HTMLFormElement): void {
    const data = new FormData(form);
    const values: FieldValues = {};
    for (const field of this.fields()) {
      if (field.advanced && !this.showAdvanced()) {
        // Untouched advanced fields keep their current values instead of
        // being dropped from the submission.
        const current = this.values()[field.name];
        if (current !== undefined) {
          values[field.name] = current;
        }
        continue;
      }
      const value = coerce(field, data);
      if (value === undefined) {
        continue;
      }
      values[field.name] = value;
    }
    this.submitted.emit(values);
  }
}

function coerce(field: ConfigField, data: FormData): unknown {
  if (field.type === 'boolean') {
    // Unchecked checkboxes submit nothing, which means false.
    return data.has(field.name);
  }
  const raw = data.get(field.name);
  if (raw === null || raw === '') {
    // Empty optionals are omitted so server defaults apply.
    return undefined;
  }
  if (field.type === 'integer') {
    const parsed = Number(raw);
    return Number.isNaN(parsed) ? raw : parsed;
  }
  return raw;
}
