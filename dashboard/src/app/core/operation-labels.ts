/** How an operation reads to the owner: the action, and its finished form. */
export interface OrderLabel {
  title: string;
  done: string;
}

/**
 * Display names for packages ("GridCraft", not "gridcraft"), filled by the
 * Catalog pages once they have loaded the catalog. Until then a package is
 * named from its slug.
 */
const packageTitles = new Map<string, string>();

export function registerPackageTitles(titles: { name: string; title: string }[]): void {
  for (const entry of titles) {
    packageTitles.set(entry.name, entry.title);
  }
}

function name(raw: string): string {
  const decoded = decodeURIComponent(raw);
  return packageTitles.get(decoded) ?? decoded.charAt(0).toUpperCase() + decoded.slice(1);
}

type Rule = [RegExp, (m: RegExpMatchArray) => OrderLabel];

/**
 * Operation-starting requests and their labels. The action keeps one name
 * through the flow: the button says "Reconcile routes", the rail says
 * "Reconcile routes" while it runs and the toast says "Routes reconciled".
 */
const RULES: Rule[] = [
  [/^POST \/api\/v1\/edge\/deploy$/, () => ({ title: 'Deploy Caddy', done: 'Caddy deployed' })],
  [/^POST \/api\/v1\/edge\/routes$/, () => ({ title: 'Reconcile routes', done: 'Routes reconciled' })],
  [/^POST \/api\/v1\/catalog\/([^/]+)\/install$/, (m) => ({ title: `Install ${name(m[1])}`, done: `${name(m[1])} installed` })],
  [/^POST \/api\/v1\/catalog\/([^/]+)\/uninstall$/, (m) => ({ title: `Uninstall ${name(m[1])}`, done: `${name(m[1])} uninstalled` })],
  [/^POST \/api\/v1\/catalog\/([^/]+)\/configure$/, (m) => ({ title: `Configure ${name(m[1])}`, done: `${name(m[1])} configured` })],
  [/^POST \/api\/v1\/catalog\/([^/]+)\/drift$/, (m) => ({ title: `Restore ${name(m[1])}`, done: `${name(m[1])} restored` })],
  [/^POST \/api\/v1\/data-migrations\/([^/]+)\/approve$/, (m) => ({ title: `Migrate ${name(m[1])} data`, done: `${name(m[1])} data migrated` })],
  [/^POST \/api\/v1\/data-migrations\/([^/]+)\/remove-volumes$/, () => ({ title: 'Remove old volumes', done: 'Old volumes removed' })],
  [/^POST \/api\/v1\/updates\/([^/]+)\/apply$/, (m) => ({ title: `Update ${name(m[1])}`, done: `${name(m[1])} updated` })],
  [/^POST \/api\/v1\/platform\/updates\/apply$/, () => ({ title: 'Apply platform update', done: 'Platform update applied' })],
  [/^POST \/api\/v1\/platform\/updates\/rollback$/, () => ({ title: 'Roll back platform', done: 'Platform rolled back' })],
  [/^POST \/api\/v1\/craft\/build$/, () => ({ title: 'Build craft app', done: 'Craft app built' })],
  [/^POST \/api\/v1\/secrets\/rekey$/, () => ({ title: 'Re-key secrets', done: 'Secrets re-keyed' })],
  [/^POST \/api\/v1\/sso\/apply$/, () => ({ title: 'Set up sign-in', done: 'Sign-in set up' })],
  [/^POST \/api\/v1\/exposure\/apply$/, () => ({ title: 'Apply exposure', done: 'Exposure applied' })],
  [/^POST \/api\/v1\/tls\/local-ca\/leaves\/reissue$/, () => ({ title: 'Reissue local certificates', done: 'Local certificates reissued' })],
  [/^POST \/api\/v1\/tls\/local-ca\/rotation$/, () => ({ title: 'Rotate local root', done: 'Local root rotated' })],
  [/^POST \/api\/v1\/tls\/issuance\/apply$/, () => ({ title: 'Issue certificate', done: 'Certificate issued' })],
  [/^POST \/api\/v1\/backups\/identity\/rewrap$/, () => ({ title: 'Re-wrap backups', done: 'Backups re-wrapped' })],
  [/^POST \/api\/v1\/tunnel\/setup$/, () => ({ title: 'Set up tunnel', done: 'Tunnel set up' })],
  [/^DELETE \/api\/v1\/tunnel\/tunnels\/[^/]+$/, () => ({ title: 'Remove tunnel', done: 'Tunnel removed' })],
];

/** Label for a request that starts an operation, or null for any other request. */
export function labelForRequest(method: string, url: string): OrderLabel | null {
  const path = url.split('?')[0];
  const key = `${method.toUpperCase()} ${path}`;
  for (const [pattern, build] of RULES) {
    const match = key.match(pattern);
    if (match) {
      return build(match);
    }
  }
  return null;
}

const TYPES: Record<string, OrderLabel> = {
  'caddy.deploy': { title: 'Deploy Caddy', done: 'Caddy deployed' },
  'edge.routes': { title: 'Reconcile routes', done: 'Routes reconciled' },
  'platform.update': { title: 'Apply platform update', done: 'Platform update applied' },
  'craft.build': { title: 'Build craft app', done: 'Craft app built' },
  'exposure.apply': { title: 'Apply exposure', done: 'Exposure applied' },
  'sso.apply': { title: 'Set up sign-in', done: 'Sign-in set up' },
  'cert.issue': { title: 'Issue certificate', done: 'Certificate issued' },
  'tls.local-ca-rotate': { title: 'Rotate local root', done: 'Local root rotated' },
  'tls.local-leaves': { title: 'Reissue local certificates', done: 'Local certificates reissued' },
  'tunnel.setup': { title: 'Set up tunnel', done: 'Tunnel set up' },
  'tunnel.teardown': { title: 'Remove tunnel', done: 'Tunnel removed' },
  'backup.run': { title: 'Back up', done: 'Backup finished' },
  'backup.rewrap': { title: 'Re-wrap backups', done: 'Backups re-wrapped' },
  'backup.job': { title: 'Run scheduled backup', done: 'Scheduled backup finished' },
  'backup.restore': { title: 'Restore backup', done: 'Backup restored' },
  'app.update': { title: 'Update app', done: 'App updated' },
  'app.setup': { title: 'Set up app', done: 'App set up' },
  'caddy.reconcile': { title: 'Reconcile routes', done: 'Routes reconciled' },
  'catalog.install': { title: 'Install app', done: 'App installed' },
  'catalog.remove': { title: 'Uninstall app', done: 'App uninstalled' },
  'catalog.configure': { title: 'Configure app', done: 'App configured' },
  'catalog.restore': { title: 'Restore app', done: 'App restored' },
  'catalog.migrate': { title: 'Migrate app data', done: 'App data migrated' },
  'storage.provision': { title: 'Set up storage', done: 'Storage set up' },
  'docker.install': { title: 'Install Docker', done: 'Docker installed' },
  'catalog.drift-scan': { title: 'Scan for drift', done: 'Drift scan finished' },
};

/** Label for an operation known only by its type (found running on load). */
export function labelForType(type: string): OrderLabel {
  const known = TYPES[type];
  if (known) {
    return known;
  }
  const words = type.replace(/[._-]+/g, ' ').trim();
  const title = words.charAt(0).toUpperCase() + words.slice(1);
  return { title: title || 'Operation', done: `${title || 'Operation'} finished` };
}
