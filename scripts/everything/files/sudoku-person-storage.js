// Homestead (S379, ADR-057): the entry point of Sudoku on the appliance.
//
// Upstream keeps the saved game, the statistics and a device id in
// localStorage and, when signed in, mirrors the saved game and statistics to
// the person's key-value storage (which the bridge scopes to person and app).
// localStorage is shared by everyone who uses the same browser profile, and
// upstream merges the local copy into the signed-in person's cloud copy, so a
// second person would receive the first person's statistics and game.
//
// Before the app starts this module therefore asks the bridge who is signed
// in, then replaces localStorage with one whose keys carry a tag derived from
// that person (the subject is hashed and never shown). If the person cannot
// be identified, localStorage is kept in memory only: nothing is shared and
// nothing is kept. Then the app is started as upstream wrote it.
const personTag = async (subject) => {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode('psud:' + subject));
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
  // The worker serves the offline page and caches build assets only (public/sw.js).
  if ('serviceWorker' in navigator) navigator.serviceWorker.register('./sw.js').catch(() => {});
  await import('../main.js');
})();
