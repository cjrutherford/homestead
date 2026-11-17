// Homestead (S377, ADR-057): the entry point of the 3D app on the appliance.
//
// Upstream keeps everything the person sets up in the browser: preferences,
// presets, favourites and themes in localStorage, and recent files, autosave
// snapshots, linked libraries and recovery in IndexedDB and the browser's
// private file system. All of that is shared by everyone who uses the same
// browser profile. Before the app starts this module therefore
//
//   1. asks the bridge who is signed in (an opaque id; it is never shown),
//   2. replaces localStorage with a copy that is loaded from, and saved to,
//      the person's own bridge storage (key-value, per person and app),
//   3. gives IndexedDB databases and the private file system a name that
//      belongs to that person, so a second person in the same profile does
//      not see the first person's recent files, snapshots or libraries.
//
// What stays in the browser (recent files, snapshots, linked libraries) is
// separated by person, not encrypted and not backed up: a project is large
// (the bridge stores at most 8 MB per file) and is meant to be saved to the
// person's own computer. Then the app is started as upstream wrote it.

interface Bridge {
  auth: { getUser(): Promise<{ uuid?: string; username?: string }> };
  kv: { get(key: string): Promise<unknown>; set(key: string, value: unknown): Promise<unknown> };
}

const SNAPSHOT_KEY = 'browser-settings/local-storage.v1';
/** Browsers allow about 5 MB; the bridge value limit is 8 MB. */
const MAX_CHARS = 3_000_000;
const SAVE_DELAY_MS = 1000;

const fnv = (s: string): string => {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 0x01000193);
  return (h >>> 0).toString(16).padStart(8, '0');
};

async function personTag(subject: string): Promise<string> {
  try {
    const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode('p3d:' + subject));
    return Array.from(new Uint8Array(digest).slice(0, 8), (b) => b.toString(16).padStart(2, '0')).join('');
  } catch {
    return fnv(subject) + fnv(subject.split('').reverse().join(''));
  }
}

/** A Storage whose contents come from, and go to, the person's bridge storage. */
export class PersonStorage implements Storage {
  [name: string]: unknown;
  private items = new Map<string, string>();
  private chars = 0;
  private dirty = false;
  private saving: Promise<void> | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private warned = false;

  constructor(private readonly bridge: Pick<Bridge, 'kv'>, initial: Record<string, string> = {}) {
    for (const [k, v] of Object.entries(initial)) {
      if (typeof v === 'string') this.put(k, v);
    }
  }

  private put(k: string, v: string) {
    this.chars += k.length + v.length - (this.items.has(k) ? k.length + this.items.get(k)!.length : 0);
    this.items.set(k, v);
  }

  get length(): number {
    return this.items.size;
  }
  key(index: number): string | null {
    return Array.from(this.items.keys())[index] ?? null;
  }
  getItem(key: string): string | null {
    const k = String(key);
    return this.items.has(k) ? this.items.get(k)! : null;
  }
  setItem(key: string, value: string): void {
    const k = String(key);
    const v = String(value);
    const grow = k.length + v.length - (this.items.has(k) ? k.length + this.items.get(k)!.length : 0);
    if (this.chars + grow > MAX_CHARS) throw new DOMException('The settings storage is full.', 'QuotaExceededError');
    this.put(k, v);
    this.schedule();
  }
  removeItem(key: string): void {
    const k = String(key);
    if (!this.items.has(k)) return;
    this.chars -= k.length + this.items.get(k)!.length;
    this.items.delete(k);
    this.schedule();
  }
  clear(): void {
    this.items.clear();
    this.chars = 0;
    this.schedule();
  }

  private schedule() {
    this.dirty = true;
    if (this.timer === null) this.timer = setTimeout(() => void this.flush(), SAVE_DELAY_MS);
  }

  /** Saves the settings now (also called when the page is hidden). */
  flush(): Promise<void> {
    if (this.timer !== null) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    if (this.saving) return this.saving.then(() => (this.dirty ? this.flush() : undefined));
    if (!this.dirty) return Promise.resolve();
    this.dirty = false;
    const items = Object.fromEntries(this.items);
    this.saving = this.bridge.kv
      .set(SNAPSHOT_KEY, { v: 1, items })
      .then(
        () => undefined,
        (e) => {
          this.dirty = true;
          if (!this.warned) {
            this.warned = true;
            console.warn('Your settings could not be saved to the appliance:', (e as { message?: string })?.message ?? e);
          }
        },
      )
      .finally(() => {
        this.saving = null;
      });
    return this.saving;
  }
}

function separateBrowserStorage(tag: string) {
  // IndexedDB: p3d-session, p3d-libraries, p3d-presets, p3d-prefs, p3d-recovery.
  if (typeof IDBFactory !== 'undefined') {
    const open = IDBFactory.prototype.open;
    const del = IDBFactory.prototype.deleteDatabase;
    IDBFactory.prototype.open = function (name: string, version?: number) {
      return open.call(this, `${name}@${tag}`, version);
    };
    IDBFactory.prototype.deleteDatabase = function (name: string) {
      return del.call(this, `${name}@${tag}`);
    };
  }
  // The browser's private file system ("Browser Storage" in the file browser).
  if (typeof StorageManager !== 'undefined' && StorageManager.prototype.getDirectory) {
    const get = StorageManager.prototype.getDirectory;
    StorageManager.prototype.getDirectory = async function () {
      const root = await get.call(this);
      return root.getDirectoryHandle(`p3d-${tag}`, { create: true });
    };
  }
}

function fail(message: string): never {
  const host = document.getElementById('app') ?? document.body;
  const p = document.createElement('p');
  p.style.cssText = 'font:16px system-ui,sans-serif;padding:2rem;max-width:40rem';
  p.textContent = message;
  host.replaceChildren(p);
  throw new Error(message);
}

/** The build puts this module ahead of the bridge script in the page; wait (briefly) for the bridge. */
async function bridgeReady(): Promise<void> {
  for (let i = 0; i < 100 && !(globalThis as { puter?: unknown }).puter; i++) {
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
}

async function boot() {
  await bridgeReady();
  const bridge = (globalThis as unknown as { puter?: Bridge }).puter;
  if (!bridge?.auth || !bridge.kv) fail('This app needs the Homestead storage service, which could not be reached. Reload the page.');
  let subject = '';
  try {
    const user = await bridge.auth.getUser();
    subject = String(user.uuid ?? user.username ?? '');
  } catch {
    fail('Your session could not be checked. Reload the page to sign in again.');
  }
  if (!subject) fail('Your session could not be checked. Reload the page to sign in again.');
  let saved: Record<string, string> = {};
  try {
    const snapshot = (await bridge.kv.get(SNAPSHOT_KEY)) as { items?: Record<string, string> } | null;
    if (snapshot && typeof snapshot.items === 'object' && snapshot.items) saved = snapshot.items;
  } catch {
    // no settings saved yet, or the service is busy: start with defaults
  }
  const storage = new PersonStorage(bridge, saved);
  Object.defineProperty(window, 'localStorage', { value: storage, configurable: true, writable: false });
  const flush = () => void storage.flush();
  window.addEventListener('pagehide', flush);
  document.addEventListener('visibilitychange', () => document.visibilityState === 'hidden' && flush());
  separateBrowserStorage(await personTag(subject));
  await import('../main');
}

// Not started when the module is imported by a test.
if (typeof document !== 'undefined' && (globalThis as { __HOMESTEAD_NO_BOOT__?: boolean }).__HOMESTEAD_NO_BOOT__ !== true) {
  void boot();
}
