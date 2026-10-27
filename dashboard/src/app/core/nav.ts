/** Primary navigation model (S282): groups, items and who may see them. */

export interface NavItem {
  label: string;
  path: string;
  /** Visible to logged-out visitors. */
  public?: boolean;
  /** Visible only while the appliance is unclaimed. */
  unclaimedOnly?: boolean;
  /** The setup walkthrough entry (S287): shown with the label the walkthrough chooses. */
  setup?: boolean;
}

export interface NavGroup {
  id: string;
  label: string;
  items: NavItem[];
}

export const NAV_GROUPS: readonly NavGroup[] = [
  {
    id: 'overview',
    label: 'Overview',
    items: [
      { label: 'Status', path: '/status', public: true },
      { label: 'Setup', path: '/setup', setup: true },
      { label: 'Claim', path: '/claim', public: true, unclaimedOnly: true },
    ],
  },
  {
    id: 'apps',
    label: 'Apps',
    items: [
      { label: 'Catalog', path: '/catalog' },
      { label: 'Craft apps', path: '/craft-apps' },
      { label: 'Craft files', path: '/craft-files' },
      { label: 'Updates', path: '/updates' },
      { label: 'Data', path: '/data' },
    ],
  },
  {
    id: 'network',
    label: 'Network',
    items: [
      { label: 'Edge', path: '/edge' },
      { label: 'Exposure', path: '/exposure' },
      { label: 'Tunnel', path: '/tunnel' },
      { label: 'Names', path: '/names' },
      { label: 'Certificates', path: '/tls' },
      { label: 'Trust', path: '/trust' },
      { label: 'Firewall', path: '/firewall' },
    ],
  },
  {
    id: 'storage',
    label: 'Storage and backups',
    items: [
      { label: 'Storage', path: '/storage' },
      { label: 'Backups', path: '/backups' },
    ],
  },
  {
    id: 'system',
    label: 'System',
    items: [
      { label: 'Configuration', path: '/config' },
      { label: 'Doctor', path: '/doctor' },
      { label: 'Drift', path: '/drift' },
      { label: 'Secrets', path: '/secrets' },
      { label: 'People', path: '/people' },
      { label: 'Sign-in', path: '/sign-in' },
      { label: 'Operations', path: '/operations' },
      { label: 'Audit', path: '/audit' },
      { label: 'Account', path: '/account' },
      { label: 'Advanced', path: '/advanced' },
    ],
  },
];

/**
 * The groups a visitor sees. Logged-out visitors get Status, and Claim
 * while the appliance is unclaimed; everything else needs a session. The
 * Setup entry (S287) appears once the walkthrough status is known, and reads
 * "Finish setup" on an appliance claimed before the walkthrough existed.
 * Empty groups are dropped.
 */
export function visibleGroups(
  loggedIn: boolean,
  unclaimed: boolean,
  setupLabel: string | null = null,
): NavGroup[] {
  return NAV_GROUPS.map((group) => ({
    ...group,
    items: group.items
      .filter(
        (item) =>
          (!item.unclaimedOnly || unclaimed) &&
          (loggedIn || item.public) &&
          (!item.setup || (loggedIn && setupLabel !== null)),
      )
      .map((item) => (item.setup && setupLabel !== null ? { ...item, label: setupLabel } : item)),
  })).filter((group) => group.items.length > 0);
}
