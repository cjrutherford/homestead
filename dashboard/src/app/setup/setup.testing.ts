import { SetupStatus } from './setup.service';

/** A fresh appliance part-way through the walkthrough (test helper). */
export function setupStatus(overrides: Partial<SetupStatus> = {}): SetupStatus {
  return {
    origin: 'fresh',
    legacy: false,
    complete: false,
    gated: true,
    current: 'account',
    steps: [
      { name: 'account', title: 'Secure the owner account', state: 'pending', detail: 'Export the recovery identity.', skipped: false, acknowledged: false },
      { name: 'storage', title: 'Storage', state: 'pending', detail: 'Assign the application storage class.', skipped: false, acknowledged: false },
      { name: 'sign-in', title: 'Sign-in', state: 'pending', detail: 'Install Authentik.', skipped: false, acknowledged: false },
      { name: 'domain', title: 'Domain and access', state: 'pending', detail: 'Choose a DNS provider.', skipped: false, acknowledged: false },
      { name: 'apps', title: 'First apps', state: 'pending', detail: 'Pick a few apps.', skipped: false, acknowledged: false },
    ],
    choices: {},
    facts: {
      recovery_exported: false,
      recovery_acknowledged: false,
      storage_app_assigned: false,
      password_confirmed: false,
      sign_in: { authentik_installed: false, configured: false, linked: false, dormant: false },
      domain: { configured: false, onboarded: false, credentials_sealed: false },
      tunnels: 0,
      exposures: 0,
      review_existing: false,
      apps_installed: 0,
    },
    providers: [
      {
        name: 'cloudflare', label: 'Cloudflare', summary: 'Homestead reads your zone and edits its DNS records.',
        credentials: [{ name: 'api_token', label: 'API token', secret: true }],
        permissions: [{ grant: 'Zone: DNS: Edit', reason: 'list, create and delete records' }],
        tunnel_permissions: [{ grant: 'Account: Cloudflare Tunnel: Edit', reason: 'create the tunnel' }],
        notes: [],
      },
      { name: 'manual', label: 'Manual', summary: 'No credential.', credentials: null, permissions: null, tunnel_permissions: null, notes: null },
    ],
    access: [
      { id: 'home', label: 'Home network only', description: 'Nothing is opened.', available: true },
      { id: 'tunnel', label: 'Cloudflare Tunnel', description: 'Outbound only.', available: false, reason: 'Choose Cloudflare as the DNS provider to use a tunnel.' },
      { id: 'router', label: 'Router port forward', description: 'Open ports.', available: true },
    ],
    starter_apps: [{ name: 'nextcloud', title: 'Nextcloud', blurb: 'Files you host.', installed: false }],
    ...overrides,
  };
}
