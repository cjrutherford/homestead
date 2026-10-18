import { describe, expect, it } from 'vitest';
import { OperationState, OperationStep } from './api.service';
import { formatElapsed, progressOf } from './operation-view';

function step(position: number, name: string, status: string): OperationStep {
  return { id: position + 1, operation_id: 1, position, name, status, started_at: '', finished_at: '', error: '' };
}
function op(status: string, steps: OperationStep[]): OperationState {
  return { id: 1, type: 'x.y', status, payload: '', result: '', error: '', created_at: '', updated_at: '', steps };
}

describe('progressOf', () => {
  it('names the current step and counts', () => {
    const p = progressOf(op('running', [step(0, 'pull', 'succeeded'), step(1, 'up', 'running'), step(2, 'check', 'pending')]));
    expect(p.text).toBe('Step 2 of 3: up');
    expect(p.done).toBe(1);
    expect(p.fraction).toBeCloseTo(1 / 3);
  });

  it('reports results', () => {
    expect(progressOf(op('succeeded', [step(0, 'a', 'succeeded')])).text).toBe('Done');
    expect(progressOf(op('failed', [step(0, 'a', 'failed')])).text).toBe('Failed at a');
    expect(progressOf(op('cancelled', [])).text).toBe('Cancelled');
    expect(progressOf(null).text).toBe('Starting');
  });
});

describe('formatElapsed', () => {
  it('formats minutes and hours', () => {
    expect(formatElapsed(5000)).toBe('0:05');
    expect(formatElapsed(125000)).toBe('2:05');
    expect(formatElapsed(3725000)).toBe('1:02:05');
  });
});
