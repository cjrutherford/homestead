/** Primary navigation model (S282): groups, items and who may see them. */

export interface NavItem {
  label: string;
  path: string;
  /** Supplied Fieldwork glyph. The visible label remains the accessible name. */
  icon?: string;
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
      { label: 'Status', path: '/admin/status', icon: 'overview', public: true },
      { label: 'Setup', path: '/setup', icon: 'review', setup: true },
      { label: 'Claim', path: '/claim', icon: 'host', public: true, unclaimedOnly: true },
    ],
  },
  {
    id: 'apps',
    label: 'Apps',
    items: [
      { label: 'Catalog', path: '/admin/catalog', icon: 'catalog' },
      { label: 'Craft apps', path: '/admin/craft-apps', icon: 'catalog' },
      { label: 'Craft files', path: '/admin/craft-files', icon: 'configuration' },
      { label: 'Updates', path: '/admin/updates', icon: 'updates' },
      { label: 'Data', path: '/admin/data', icon: 'storage' },
    ],
  },
  {
    id: 'network',
    label: 'Network',
    items: [
      { label: 'Edge', path: '/admin/edge', icon: 'network' },
      { label: 'Exposure', path: '/admin/exposure', icon: 'public-route' },
      { label: 'Tunnel', path: '/admin/tunnel', icon: 'network' },
      { label: 'Names', path: '/admin/names', icon: 'network' },
      { label: 'Certificates', path: '/admin/tls', icon: 'access' },
      { label: 'Trust', path: '/admin/trust', icon: 'access' },
      { label: 'Firewall', path: '/admin/firewall', icon: 'public-route' },
    ],
  },
  {
    id: 'storage',
    label: 'Storage and backups',
    items: [
      { label: 'Storage', path: '/admin/storage', icon: 'storage' },
      { label: 'Backups', path: '/admin/backups', icon: 'storage' },
    ],
  },
  {
    id: 'system',
    label: 'System',
    items: [
      { label: 'Configuration', path: '/admin/config', icon: 'configuration' },
      { label: 'Doctor', path: '/admin/doctor', icon: 'host' },
      { label: 'Drift', path: '/admin/drift', icon: 'activity' },
      { label: 'Secrets', path: '/admin/secrets', icon: 'access' },
      { label: 'People', path: '/admin/people', icon: 'people' },
      { label: 'Sign-in', path: '/sign-in', icon: 'access' },
      { label: 'Operations', path: '/admin/operations', icon: 'activity' },
      { label: 'Audit', path: '/admin/audit', icon: 'activity' },
      { label: 'Account', path: '/account', icon: 'people' },
      { label: 'Advanced', path: '/admin/advanced', icon: 'configuration' },
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
