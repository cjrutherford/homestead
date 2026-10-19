import { describe, expect, it } from 'vitest';
import { ariaSort, nextSort, sortRows } from './sort';

describe('sort helpers', () => {
  it('cycles a column ascending then descending and resets on a new column', () => {
    const first = nextSort(null, 'name');
    expect(first).toEqual({ key: 'name', dir: 'asc' });
    expect(nextSort(first, 'name')).toEqual({ key: 'name', dir: 'desc' });
    expect(nextSort(first, 'size')).toEqual({ key: 'size', dir: 'asc' });
  });

  it('reports aria-sort only for the sorted column', () => {
    const state = { key: 'name', dir: 'desc' as const };
    expect(ariaSort(state, 'name')).toBe('descending');
    expect(ariaSort(state, 'size')).toBe('none');
    expect(ariaSort(null, 'name')).toBe('none');
  });

  it('sorts text naturally and numbers numerically, without mutating the input', () => {
    const rows = [{ n: 'b10', s: 2 }, { n: 'b2', s: 10 }];
    const pick = (r: { n: string; s: number }, k: string) => (k === 's' ? r.s : r.n);
    expect(sortRows(rows, { key: 'n', dir: 'asc' }, pick).map((r) => r.n)).toEqual(['b2', 'b10']);
    expect(sortRows(rows, { key: 's', dir: 'desc' }, pick).map((r) => r.s)).toEqual([10, 2]);
    expect(sortRows(rows, null, pick)).toEqual(rows);
    expect(rows[0].n).toBe('b10');
  });
});
