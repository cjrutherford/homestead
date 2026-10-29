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
      { label: 'Status', path: '/admin/status', public: true },
      { label: 'Setup', path: '/setup', setup: true },
      { label: 'Claim', path: '/claim', public: true, unclaimedOnly: true },
    ],
  },
  {
    id: 'apps',
    label: 'Apps',
    items: [
      { label: 'Catalog', path: '/admin/catalog' },
      { label: 'Craft apps', path: '/admin/craft-apps' },
      { label: 'Craft files', path: '/admin/craft-files' },
      { label: 'Updates', path: '/admin/updates' },
      { label: 'Data', path: '/admin/data' },
    ],
  },
  {
    id: 'network',
    label: 'Network',
    items: [
      { label: 'Edge', path: '/admin/edge' },
      { label: 'Exposure', path: '/admin/exposure' },
      { label: 'Tunnel', path: '/admin/tunnel' },
      { label: 'Names', path: '/admin/names' },
      { label: 'Certificates', path: '/admin/tls' },
      { label: 'Trust', path: '/admin/trust' },
      { label: 'Firewall', path: '/admin/firewall' },
    ],
  },
  {
    id: 'storage',
    label: 'Storage and backups',
    items: [
      { label: 'Storage', path: '/admin/storage' },
      { label: 'Backups', path: '/admin/backups' },
    ],
  },
  {
    id: 'system',
    label: 'System',
    items: [
      { label: 'Configuration', path: '/admin/config' },
      { label: 'Doctor', path: '/admin/doctor' },
      { label: 'Drift', path: '/admin/drift' },
      { label: 'Secrets', path: '/admin/secrets' },
      { label: 'People', path: '/admin/people' },
      { label: 'Sign-in', path: '/sign-in' },
      { label: 'Operations', path: '/admin/operations' },
      { label: 'Audit', path: '/admin/audit' },
      { label: 'Account', path: '/account' },
      { label: 'Advanced', path: '/admin/advanced' },
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
