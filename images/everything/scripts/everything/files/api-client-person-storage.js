// Homestead (S436, ADR-057): the entry point of API Client on the appliance.
//
// Upstream keeps preferences, the "last user" record and a few flags in
// localStorage, and its workspace in IndexedDB (a database per user id).
// localStorage is shared by everyone who uses the same browser profile, so the
// keys get a tag derived from the signed-in person (hashed, never shown); the
// IndexedDB name already carries the person's id from the bridge. If the
// person cannot be identified, localStorage is kept in memory only. The
// workspace is also mirrored to the bridge's per-person storage by upstream's
// own sync. Then the app is started as upstream wrote it.
const personTag = async (subject) => {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode('pacl:' + subject));
  return Array.from(new Uint8Array(digest).slice(0, 8), (b) => b.toString(16).padStart(2, '0')).join('');
};

const memoryStorage = () => {
  const items = new Map();
  return {
    get length() { return items.size; },
    key: (i) => Array.from(items.keys())[i] ?? null,
    getItem: (k) => (items.has(String(k)) ? items.get(String(k)) : null),
    setItem: (k, v) => { items.set(String(k), String(v)); },
    removeItem: (k) => { items.delete(String(k)); },
    clear: () => { items.clear(); },
  };
};

const taggedStorage = (real, tag) => {
  const prefix = 'hs:' + tag + ':';
  const own = () => Object.keys(real).filter((k) => k.startsWith(prefix));
  return {
    get length() { return own().length; },
    key: (i) => { const k = own()[i]; return k === undefined ? null : k.slice(prefix.length); },
    getItem: (k) => real.getItem(prefix + String(k)),
    setItem: (k, v) => real.setItem(prefix + String(k), String(v)),
    removeItem: (k) => real.removeItem(prefix + String(k)),
    clear: () => { own().forEach((k) => real.removeItem(k)); },
  };
};

(async () => {
  let storage = memoryStorage();
  try {
    const user = await window.puter.auth.getUser();
    const subject = String((user && (user.uuid || user.username)) || '');
    if (subject) storage = taggedStorage(window.localStorage, await personTag(subject));
  } catch {
    // keep the in-memory storage
  }
  Object.defineProperty(window, 'localStorage', { value: storage, configurable: true });
  await import('../main.js');
})();
