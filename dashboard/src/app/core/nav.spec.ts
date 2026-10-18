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
    expect(labels(true, false)).toEqual(all.filter((label) => label !== 'Claim'));
    expect(labels(true, true)).toEqual(all);
  });

  it('keeps every item path unique', () => {
    const paths = NAV_GROUPS.flatMap((g) => g.items.map((i) => i.path));
    expect(new Set(paths).size).toBe(paths.length);
  });
});
