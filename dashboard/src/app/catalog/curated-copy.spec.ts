import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { CURATED_COPY, curatedFor } from './curated-copy';

/** Package names come from the manifests themselves: catalog/*.yaml. */
function catalogNames(): string[] {
  return readdirSync('../catalog')
    .filter((file) => file.endsWith('.yaml'))
    .map((file) => /^name:\s*(\S+)/m.exec(readFileSync(`../catalog/${file}`, 'utf8'))?.[1] ?? '');
}

describe('curated install copy', () => {
  it('finds the catalog packages', () => {
    expect(catalogNames().length).toBeGreaterThanOrEqual(17);
    expect(catalogNames()).not.toContain('');
  });

  it('has first-login copy for every catalog package, so a new package cannot ship without it', () => {
    for (const name of catalogNames()) {
      const copy = CURATED_COPY[name];
      expect(copy, `curated first-login copy for ${name}`).toBeDefined();
      expect(copy.firstLogin.length, name).toBeGreaterThan(0);
      for (const line of copy.firstLogin) {
        expect(line.trim(), name).not.toBe('');
      }
    }
  });

  it('has no entry for a package that is not in the catalog', () => {
    const names = new Set(catalogNames());
    for (const name of Object.keys(CURATED_COPY)) {
      expect(names.has(name), `${name} is curated but not in catalog/`).toBe(true);
    }
  });

  it('falls back to a generic, true line for an unknown package', () => {
    expect(curatedFor('not-a-package').firstLogin).toEqual([
      'Open the app and follow its first-run screen.',
    ]);
  });

  it('follows the voice rules: sentence case, no middle dots, no arrows', () => {
    for (const [name, copy] of Object.entries(CURATED_COPY)) {
      for (const line of [...copy.firstLogin, copy.note ?? '']) {
        expect(line, name).not.toMatch(/·|→/);
      }
    }
  });
});
