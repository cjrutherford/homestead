/** Field value types the renderer handles (S015 type set). */
export type FieldType = 'string' | 'integer' | 'boolean' | 'choice';

/** One field of server field metadata (GET /api/v1/config/schema). */
export interface ConfigField {
  name: string;
  type: FieldType;
  description: string;
  group?: string;
  required?: boolean;
  default?: unknown;
  choices?: string[];
  min?: number;
  max?: number;
  sensitive?: boolean;
  advanced?: boolean;
}

/** Field metadata envelope. */
export interface ConfigSchema {
  fields: ConfigField[];
}

/** Per-field validation failure (422 responses). */
export interface FieldError {
  field: string;
  reason: string;
  value?: unknown;
}

/** Setting values keyed by field name. */
export type FieldValues = Record<string, unknown>;
