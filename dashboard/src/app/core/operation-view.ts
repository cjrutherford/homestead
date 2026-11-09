import { OperationState, OperationStep } from './api.service';

const TERMINAL = new Set(['succeeded', 'failed', 'cancelled']);

export function isTerminal(status: string): boolean {
  return TERMINAL.has(status);
}

export interface OperationProgress {
  /** Steps finished (succeeded). */
  done: number;
  total: number;
  /** Zero-based index of the step in progress, or null when none is. */
  currentIndex: number | null;
  currentName: string;
  /** Plain summary: "Step 2 of 4: Pull images", "Done", "Failed". */
  text: string;
  /** 0 to 1. */
  fraction: number;
}

/**
 * Daemon step names are often slugs ("obtain-artifact", "verify-craft-images").
 * Show them as words; names that already read as words are left alone.
 */
const STEP_WORDS: Record<string, string> = {
  'ensure-homestead-edge': 'Create the edge network',
  'check-existing-database': 'Check for an existing database',
  'verify-craft-images': 'Check the built craft image',
  'write-stack': 'Write the stack files',
  up: 'Start the containers',
  down: 'Stop the containers',
  'remove-files': 'Remove the generated files',
  'remove-data': 'Delete the data',
};

export function stepLabel(name: string): string {
  if (!name || /\s/.test(name)) {
    return name;
  }
  if (STEP_WORDS[name]) {
    return STEP_WORDS[name];
  }
  if (name.startsWith('provision:')) {
    return `Prepare folder ${name.slice('provision:'.length)}`;
  }
  const words = name.replace(/[-_.]+/g, ' ').trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export function currentStep(steps: OperationStep[]): number | null {
  const running = steps.findIndex((step) => step.status === 'running');
  if (running >= 0) {
    return running;
  }
  const waiting = steps.findIndex((step) => step.status === 'pending');
  return waiting >= 0 ? waiting : null;
}

/** Reduces an operation record to what the rail, detail page and in-page progress all say. */
export function progressOf(op: OperationState | null): OperationProgress {
  const steps = op?.steps ?? [];
  const total = steps.length;
  const done = steps.filter((step) => step.status === 'succeeded').length;
  if (!op) {
    return { done: 0, total: 0, currentIndex: null, currentName: '', text: 'Starting', fraction: 0 };
  }
  if (op.status === 'succeeded') {
    return { done: total, total, currentIndex: null, currentName: '', text: 'Done', fraction: 1 };
  }
  if (op.status === 'failed') {
    const failed = steps.find((step) => step.status === 'failed');
    return {
      done,
      total,
      currentIndex: null,
      currentName: stepLabel(failed?.name ?? ''),
      text: failed ? `Failed at ${stepLabel(failed.name)}` : 'Failed',
      fraction: total ? done / total : 0,
    };
  }
  if (op.status === 'cancelled') {
    return { done, total, currentIndex: null, currentName: '', text: 'Cancelled', fraction: total ? done / total : 0 };
  }
  const index = currentStep(steps);
  if (index === null) {
    return { done, total, currentIndex: null, currentName: '', text: 'Starting', fraction: total ? done / total : 0 };
  }
  return {
    done,
    total,
    currentIndex: index,
    currentName: stepLabel(steps[index].name),
    text: `Step ${index + 1} of ${total}: ${stepLabel(steps[index].name)}`,
    fraction: total ? done / total : 0,
  };
}

/** Elapsed time as m:ss, or h:mm:ss past an hour. */
export function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const seconds = total % 60;
  const minutes = Math.floor(total / 60) % 60;
  const hours = Math.floor(total / 3600);
  const pad = (n: number) => String(n).padStart(2, '0');
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
}
