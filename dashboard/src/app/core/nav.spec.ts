import { describe, expect, it } from 'vitest';
import { NAV_GROUPS, visibleGroups } from './nav';

const labels = (loggedIn: boolean, unclaimed: boolean) =>
  visibleGroups(loggedIn, unclaimed).flatMap((g) => g.items.map((i) => i.label));

describe('navigation model', () => {
  it('shows a logged-out visitor Status only, plus Claim while unclaimed', () => {
    expect(labels(false, false)).toEqual(['Status']);
    expect(labels(false, true)).toEqual(['Status', 'Claim']);
    expect(visibleGroups(false, false).map((g) => g.label)).toEqual(['Overview']);
  });

  it('shows a logged-in owner every page except Claim on a claimed appliance', () => {
    const all = NAV_GROUPS.flatMap((g) => g.items.map((i) => i.label));
    expect(labels(true, false)).toEqual(all.filter((label) => label !== 'Claim' && label !== 'Setup'));
    expect(labels(true, true)).toEqual(all.filter((label) => label !== 'Setup'));
  });

  it('offers Setup only to a logged-in owner once the walkthrough status is known (S287)', () => {
    const names = (loggedIn: boolean, label: string | null) =>
      visibleGroups(loggedIn, false, label).flatMap((g) => g.items.map((i) => i.label));
    expect(names(true, 'Setup')).toContain('Setup');
    expect(names(true, 'Finish setup')).toContain('Finish setup');
    expect(names(true, 'Finish setup')).not.toContain('Setup');
    expect(names(true, null)).not.toContain('Setup');
    expect(names(false, 'Setup')).toEqual(['Status']);
    expect(names(true, 'Setup').indexOf('Setup')).toBe(1);
  });

  it('keeps every item path unique', () => {
    const paths = NAV_GROUPS.flatMap((g) => g.items.map((i) => i.path));
    expect(new Set(paths).size).toBe(paths.length);
  });
});
