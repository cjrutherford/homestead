import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { contrast } from './contrast';

const css = readFileSync('src/tokens.css', 'utf8');

/** Declarations of the first rule whose selector list contains `selector`. */
function block(selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {`);
  expect(start, `selector ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf('{', start);
  const body = css.slice(open + 1, css.indexOf('}', open));
  const out: Record<string, string> = {};
  for (const match of body.matchAll(/(--[a-z-]+):\s*([^;]+?);/g)) {
    out[match[1]] = match[2].replace(/\s*\/\*.*$/, '').trim();
  }
  return out;
}

const themes = {
  light: block(":root,\n[data-theme='light']"),
  dark: block("[data-theme='dark']"),
  'high-contrast': block("[data-theme='high-contrast']"),
};

/** [foreground token, background token, minimum ratio, what it is]. */
const TEXT = 4.5;
const NON_TEXT = 3;
const pairs: [string, string, number][] = [
  ['--foreground', '--background', TEXT],
  ['--foreground', '--surface', TEXT],
  ['--foreground', '--surface-sunken', TEXT],
  ['--foreground', '--surface-raised', TEXT],
  ['--muted', '--background', TEXT],
  ['--muted', '--surface', TEXT],
  ['--muted', '--surface-sunken', TEXT],
  ['--muted', '--pending-bg', TEXT],
  ['--link', '--background', TEXT],
  ['--link', '--surface', TEXT],
  ['--link-hover', '--surface', TEXT],
  ['--primary-foreground', '--primary', TEXT],
  ['--primary-foreground', '--primary-hover', TEXT],
  ['--primary-foreground', '--primary-active', TEXT],
  ['--primary', '--background', TEXT],
  ['--primary', '--surface', TEXT],
  ['--danger-foreground', '--danger', TEXT],
  ['--danger-foreground', '--danger-hover', TEXT],
  ['--danger-foreground', '--danger-active', TEXT],
  ['--danger', '--background', TEXT],
  ['--danger', '--surface', TEXT],
  ['--danger', '--danger-bg', TEXT],
  ['--warn-text', '--background', TEXT],
  ['--warn-text', '--surface', TEXT],
  ['--warn-text', '--warn-bg', TEXT],
  ['--success', '--background', TEXT],
  ['--success', '--surface', TEXT],
  ['--success', '--success-bg', TEXT],
  ['--running', '--running-bg', TEXT],
  ['--sidebar-foreground', '--sidebar-bg', TEXT],
  ['--sidebar-muted', '--sidebar-bg', TEXT],
  ['--sidebar-foreground', '--sidebar-hover-bg', TEXT],
  ['--sidebar-active-foreground', '--sidebar-active-bg', TEXT],
  // Non-text: control borders, focus rings, status glyph colours.
  ['--border-strong', '--background', NON_TEXT],
  ['--border-strong', '--surface', NON_TEXT],
  ['--focus-ring', '--background', NON_TEXT],
  ['--focus-ring', '--surface', NON_TEXT],
  ['--focus-ring', '--surface-raised', NON_TEXT],
  ['--sidebar-focus', '--sidebar-bg', NON_TEXT],
  ['--rail-foreground', '--rail-bg', TEXT],
  ['--rail-fill', '--rail-track', NON_TEXT],
  ['--rail-border', '--sidebar-bg', NON_TEXT],
  ['--rail-foreground', '--sidebar-bg', TEXT],
  // S309: the Hedgerow band, its notch, the masthead rule and the Needs you tint.
  ['--on-hedgerow', '--hedgerow', TEXT],
  ['--masthead-rule', '--background', NON_TEXT],
  ['--foreground', '--warn-surface', TEXT],
  ['--muted', '--warn-surface', TEXT],
  ['--warn-text', '--warn-surface', TEXT],
  ['--link', '--warn-surface', TEXT],
  ['--warn', '--warn-surface', NON_TEXT],
  ['--focus-ring', '--warn-surface', NON_TEXT],
  // Field furrows: the state edge on a Board panel.
  ['--success', '--surface', NON_TEXT],
  ['--danger', '--surface', NON_TEXT],
  ['--warn', '--background', NON_TEXT],
  ['--warn', '--surface', NON_TEXT],
  ['--warn', '--warn-bg', NON_TEXT],
];

/**
 * The permit (S309) is read with the tokens in force inside it: the theme's,
 * plus, in high contrast, the palette scoped to `.permit` (a white document).
 */
const permitScope = block("[data-theme='high-contrast'] .permit");
const permitThemes: Record<string, Record<string, string>> = {
  light: themes.light,
  dark: themes.dark,
  'high-contrast': { ...themes['high-contrast'], ...permitScope },
};
const permitPairs: [string, string, number][] = [
  ['--on-manila', '--manila', TEXT],
  ['--on-manila-muted', '--manila', TEXT],
  ['--stamp', '--manila', TEXT],
  ['--stamp-refused', '--manila', TEXT],
  ['--link', '--manila', TEXT],
  ['--danger', '--manila', TEXT],
  ['--warn-text', '--manila', TEXT],
  ['--success', '--manila', TEXT],
  ['--primary-foreground', '--primary', TEXT],
  ['--danger-foreground', '--danger', TEXT],
  ['--foreground', '--surface', TEXT],
  ['--muted', '--surface', TEXT],
  ['--foreground', '--surface-sunken', TEXT],
  ['--warn-text', '--warn-bg', TEXT],
  ['--danger', '--danger-bg', TEXT],
  ['--success', '--success-bg', TEXT],
  ['--link', '--surface', TEXT],
  ['--manila-rule', '--manila', NON_TEXT],
  ['--focus-ring', '--manila', NON_TEXT],
  ['--border-strong', '--manila', NON_TEXT],
  ['--primary', '--manila', NON_TEXT],
  ['--warn', '--manila', NON_TEXT],
];

describe('permit tokens', () => {
  for (const [name, tokens] of Object.entries(permitThemes)) {
    describe(name, () => {
      for (const [fg, bg, min] of permitPairs) {
        it(`${fg} on ${bg} is at least ${min}:1`, () => {
          const ratio = contrast(tokens[fg], tokens[bg]);
          expect(ratio, `${fg} ${tokens[fg]} on ${bg} ${tokens[bg]}`).toBeGreaterThanOrEqual(min);
        });
      }
    });
  }

  it('keeps the approved permit palette', () => {
    expect(themes.light['--manila']).toBe('#f5eedb');
    expect(themes.dark['--manila']).toBe('#2a2619');
    expect(themes.light['--hedgerow']).toBe('#12291f');
    expect(themes.light['--stamp']).toBe(themes.light['--primary']);
    expect(themes.light['--stamp-refused']).toBe(themes.light['--danger']);
    expect(themes['high-contrast']['--manila']).toBe('#ffffff');
    expect(themes['high-contrast']['--stamp']).toBe('#000000');
  });
});

describe('theme tokens', () => {
  for (const [name, tokens] of Object.entries(themes)) {
    describe(name, () => {
      for (const [fg, bg, min] of pairs) {
        it(`${fg} on ${bg} is at least ${min}:1`, () => {
          const ratio = contrast(tokens[fg], tokens[bg]);
          expect(ratio, `${fg} ${tokens[fg]} on ${bg} ${tokens[bg]}`).toBeGreaterThanOrEqual(min);
        });
      }
    });
  }

  it('declares the same tokens in every theme', () => {
    const names = Object.keys(themes.light).filter((key) => key !== '--shadow-sm').sort();
    for (const [name, tokens] of Object.entries(themes)) {
      expect(Object.keys(tokens).sort(), name).toEqual(Object.keys(themes.light).sort());
    }
    expect(names.length).toBeGreaterThan(30);
  });

  it('keeps the system-dark block identical to the dark theme', () => {
    const media = css.indexOf('@media (prefers-color-scheme: dark)');
    const system = block(':root:not([data-theme])');
    expect(css.indexOf(':root:not([data-theme])')).toBeGreaterThan(media);
    expect(system).toEqual(themes.dark);
  });

  it('keeps the approved light palette', () => {
    const light = themes.light;
    expect(light['--background']).toBe('#f2f4f0');
    expect(light['--surface']).toBe('#ffffff');
    expect(light['--primary']).toBe('#1f4d3a');
    expect(light['--foreground']).toBe('#1c2621');
    expect(light['--warn']).toBe('#b4690e');
    expect(light['--danger']).toBe('#9e2f1e');
  });

  it('makes the ok colour distinguishable from the primary colour', () => {
    for (const [name, tokens] of Object.entries(themes)) {
      expect(tokens['--success'], name).not.toBe(tokens['--primary']);
    }
    expect(contrast(themes.light['--success'], themes.light['--primary'])).toBeGreaterThan(1.1);
  });
});
