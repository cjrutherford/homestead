import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';

/*
 * S311: lamplight on every page. Walks every routed page's template and
 * fails when the page has no masthead, when a section sits flat on the
 * ground instead of on a field (plate), or when page CSS writes a raw
 * shadow or colour. The aim is that the S310 gap, where only some pages got
 * the depths, cannot recur: a new route must be listed here, and its
 * template must follow the language to pass.
 */

const APP = 'src/app/';

/** Every routed path and the template of the page it renders. */
const PAGES: Record<string, string> = {
  status: 'status/status.html',
  setup: 'setup/setup.html',
  claim: 'claim/claim.html',
  login: 'login/login.html',
  'sign-in': 'sso/sign-in.html',
  account: 'account/account.html',
  config: 'config/config.html',
  firewall: 'firewall/firewall.html',
  catalog: 'catalog/catalog.html',
  'catalog/:name/install': 'catalog/install-wizard.html',
  updates: 'updates/updates.html',
  data: 'data/data-migration.html',
  advanced: 'advanced/advanced.html',
  drift: 'drift/drift.html',
  exposure: 'exposure/exposure.html',
  tunnel: 'tunnel/tunnel.html',
  names: 'names/names.html',
  backups: 'backups/backups.html',
  storage: 'storage/storage.html',
  edge: 'edge/edge.html',
  tls: 'tls/tls.html',
  trust: 'trust/trust.html',
  'craft-apps': 'craftapps/craft-apps.html',
  'craft-files': 'craftfiles/craft-files.html',
  audit: 'audit/audit.html',
  secrets: 'secrets/secrets.html',
  operations: 'operations/operations.html',
  'operations/:id': 'operations/operation-detail.html',
  'validation-board': 'validation/validation-board.html',
  '**': 'not-found/not-found.ts',
};

/** Pages a routed page renders as its own body (each also has a masthead). */
const SUBPAGES = ['updates/platform-updates.html', 'updates/craft-images.html'];

/** Elements a page may place directly on the ground, and why. */
const GROUND: [string, string][] = [
  ['header.masthead', 'the masthead sits on the contour band'],
  ['.field-panel', 'a field is the plate every section lives on'],
  ['.gate', 'claim and login are one centred, lifted plate'],
  ['app-permit', 'a plan review is lifted paper, above any plate'],
  ['app-dialog', 'a dialog is lifted above the page'],
  ['app-reauth-dialog', 'a dialog is lifted above the page'],
  ['app-operation-progress', 'progress is itself a plate (feedback.css)'],
  ['app-platform-updates', 'a sub-page with its own masthead and fields (checked below)'],
  ['app-craft-images', 'a sub-page with its own masthead and fields (checked below)'],
  ['app-state-message:not([kind="empty"])', 'page-level errors and notices are messages, not sections'],
  ['p', 'one line of page prose under the masthead'],
];

/** Wrappers that are not boxes of their own: their children are on the ground. */
const TRANSPARENT = ['section:not([class])', 'app-loading', 'app-wizard-shell', 'ng-container'];

/** Elements whose h2 is their own heading, not a page section. */
const OWN_HEADINGS = 'app-permit, app-dialog, .permit, dialog';

function source(file: string): string {
  const text = readFileSync(APP + file, 'utf8');
  if (!file.endsWith('.ts')) return text;
  const inline = /template:\s*`([\s\S]*?)`/.exec(text);
  expect(inline, `${file} has an inline template`).not.toBeNull();
  return inline![1];
}

/** Parses an Angular template; self-closing components get closing tags. */
function parse(template: string): HTMLElement {
  const html = template.replace(
    /<((?:app|ng)-[\w-]+)((?:\s+[^\s=>/]+(?:="[^"]*")?)*)\s*\/>/g,
    '<$1$2></$1>',
  );
  const doc = new DOMParser().parseFromString(`<body>${html}</body>`, 'text/html');
  return doc.body;
}

function describeEl(el: Element): string {
  const cls = el.getAttribute('class');
  return `<${el.tagName.toLowerCase()}${cls ? ` class="${cls}"` : ''}>`;
}

/** Elements that sit flat on the ground in this template. */
function flat(root: Element): string[] {
  const out: string[] = [];
  const walk = (el: Element) => {
    for (const child of Array.from(el.children)) {
      if (GROUND.some(([selector]) => child.matches(selector))) continue;
      if (TRANSPARENT.some((selector) => child.matches(selector))) {
        walk(child);
        continue;
      }
      out.push(describeEl(child));
    }
  };
  walk(root);
  return out;
}

/** h2s that do not open a field of their own. */
function looseHeadings(root: Element): string[] {
  return Array.from(root.querySelectorAll('h2'))
    .filter((h2) => !h2.closest(OWN_HEADINGS))
    .filter((h2) => {
      const parent = h2.parentElement!;
      const opens = parent.matches('.field-panel') && parent.firstElementChild === h2;
      return !opens && !h2.closest('.gate');
    })
    .map((h2) => h2.textContent!.trim().replace(/\s+/g, ' '));
}

describe('lamplight on every page (S311)', () => {
  it('lists every routed path', () => {
    const routes = readFileSync(APP + 'app.routes.ts', 'utf8');
    const paths = Array.from(routes.matchAll(/path:\s*'([^']*)'/g), (m) => m[1]).filter(
      (path) => path !== '',
    );
    expect([...paths].sort()).toEqual(Object.keys(PAGES).sort());
  });

  for (const [path, file] of Object.entries(PAGES)) {
    describe(`/${path}`, () => {
      const body = parse(source(file));

      it('opens with the masthead: h1 and the lead sentence', () => {
        const masthead = body.querySelector('header.masthead');
        expect(masthead, `${file} has a masthead`).not.toBeNull();
        expect(masthead!.querySelector('h1')).not.toBeNull();
        expect(masthead!.querySelector('.masthead-fact')).not.toBeNull();
      });

      it('puts every section on a field, not on the ground', () => {
        expect(flat(body), `${file}: elements flat on the ground`).toEqual([]);
      });

      it('opens every section heading on its own field', () => {
        expect(looseHeadings(body), `${file}: h2s outside a field`).toEqual([]);
      });

      it('keeps empty states in wells inside a field', () => {
        const empties = Array.from(body.querySelectorAll('app-state-message[kind="empty"]'));
        const loose = empties.filter((el) => !el.closest('.field-panel, .gate, app-permit'));
        expect(loose.map(describeEl), `${file}: empty states on the ground`).toEqual([]);
      });
    });
  }

  for (const file of SUBPAGES) {
    it(`${file} follows the same rules`, () => {
      const body = parse(source(file));
      expect(body.querySelector('header.masthead h1')).not.toBeNull();
      expect(flat(body)).toEqual([]);
      expect(looseHeadings(body)).toEqual([]);
    });
  }
});

/*
 * Page CSS: depth and colour come from tokens.css only. A shadow is built
 * from var() tokens (and `inset` / `0` around a furrow); a colour is a
 * var(), a keyword or a forced-colours system colour.
 */
const CSS_ALLOW: Record<string, string> = {
  'tokens.css': 'the one place values live',
  'fonts.css': 'font faces only',
};

function pageStyles(): [string, string][] {
  const files: [string, string][] = readdirSync('src')
    .filter((name) => name.endsWith('.css') && !(name in CSS_ALLOW))
    .map((name) => [name, readFileSync('src/' + name, 'utf8')]);
  const walk = (dir: string) => {
    // Directories under src/app have no dot in their names; files do.
    for (const name of readdirSync(dir)) {
      const path = `${dir}/${name}`;
      if (!name.includes('.')) walk(path);
      else if (name.endsWith('.css')) files.push([path, readFileSync(path, 'utf8')]);
      else if (name.endsWith('.ts') && !name.endsWith('.spec.ts')) {
        const styles = /styles:\s*\[([\s\S]*?)\]\s*,?\s*\n\s*(?:\w+:|\})/.exec(
          readFileSync(path, 'utf8'),
        );
        if (styles) files.push([path, styles[1]]);
      }
    }
  };
  walk('src/app');
  return files;
}

describe('page CSS uses tokens only (S311)', () => {
  const styles = pageStyles();

  it('finds the page stylesheets', () => {
    expect(styles.map(([name]) => name)).toEqual(
      expect.arrayContaining(['base.css', 'pages.css', 'primitives.css', 'permit.css']),
    );
  });

  it('writes no colour literal', () => {
    const found = styles.flatMap(([name, text]) =>
      Array.from(
        text
          .replace(/\/\*[\s\S]*?\*\//g, '')
          .replace(/url\([^)]*\)/g, '')
          .matchAll(/#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(/g),
        (m) => `${name}: ${m[0]}`,
      ),
    );
    expect(found).toEqual([]);
  });

  it('writes no raw shadow', () => {
    const found = styles.flatMap(([name, text]) =>
      Array.from(text.matchAll(/(?:box|text)-shadow:\s*([^;}]+)/g), (m) => m[1].trim())
        .filter(
          (value) =>
            value
              .replace(/var\(--[a-z-]+\)/g, '')
              .replace(/\b(?:none|inset)\b/g, '')
              .replace(/\b0\b/g, '')
              .replace(/[\s,]/g, '') !== '',
        )
        .map((value) => `${name}: ${value}`),
    );
    expect(found).toEqual([]);
  });
});
