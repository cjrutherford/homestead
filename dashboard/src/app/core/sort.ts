/** Column sorting for data tables (S284). Pure helpers; a page owns the signal. */
export type SortDir = 'asc' | 'desc';

export interface SortState {
  key: string;
  dir: SortDir;
}

/** Clicking a column sorts it ascending, then descending, then ascending again. */
export function nextSort(current: SortState | null, key: string): SortState {
  if (current?.key === key) {
    return { key, dir: current.dir === 'asc' ? 'desc' : 'asc' };
  }
  return { key, dir: 'asc' };
}

/** Value for a header's `aria-sort`. */
export function ariaSort(current: SortState | null, key: string): 'ascending' | 'descending' | 'none' {
  if (current?.key !== key) return 'none';
  return current.dir === 'asc' ? 'ascending' : 'descending';
}

/** A sorted copy. Without a sort the input order is kept (the daemon's order). */
export function sortRows<T>(
  rows: readonly T[],
  sort: SortState | null,
  pick: (row: T, key: string) => string | number,
): T[] {
  if (!sort) return [...rows];
  const sign = sort.dir === 'asc' ? 1 : -1;
  return [...rows].sort((a, b) => {
    const left = pick(a, sort.key);
    const right = pick(b, sort.key);
    if (typeof left === 'number' && typeof right === 'number') {
      return (left - right) * sign;
    }
    return String(left).localeCompare(String(right), undefined, { numeric: true }) * sign;
  });
}
