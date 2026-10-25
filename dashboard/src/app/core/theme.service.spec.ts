import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { THEME_PREFERENCES, THEME_STORAGE_KEY, ThemeService } from './theme.service';

describe('ThemeService', () => {
  const root = document.documentElement;

  beforeEach(() => {
    localStorage.clear();
    root.removeAttribute('data-theme');
    TestBed.resetTestingModule();
  });

  afterEach(() => {
    localStorage.clear();
    root.removeAttribute('data-theme');
  });

  it('is dark by default, whatever the system says: no data-theme attribute', () => {
    const service = TestBed.inject(ThemeService);
    expect(service.preference()).toBe('dark');
    expect(root.hasAttribute('data-theme')).toBe(false);
  });

  it('offers Dark, Light, Match my system, High contrast in that order', () => {
    expect(THEME_PREFERENCES.map((p) => p.label)).toEqual(['Dark', 'Light', 'Match my system', 'High contrast']);
  });

  it('applies and persists a manual choice', () => {
    const service = TestBed.inject(ThemeService);
    for (const choice of ['light', 'system', 'high-contrast'] as const) {
      service.set(choice);
      expect(root.getAttribute('data-theme')).toBe(choice);
      expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe(choice);
    }
  });

  it('returns to dark and forgets the choice', () => {
    const service = TestBed.inject(ThemeService);
    service.set('light');
    service.set('dark');
    expect(root.hasAttribute('data-theme')).toBe(false);
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBeNull();
  });

  it('restores a stored choice and ignores junk', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'high-contrast');
    expect(TestBed.inject(ThemeService).preference()).toBe('high-contrast');
    expect(root.getAttribute('data-theme')).toBe('high-contrast');

    TestBed.resetTestingModule();
    root.removeAttribute('data-theme');
    localStorage.setItem(THEME_STORAGE_KEY, 'neon');
    expect(TestBed.inject(ThemeService).preference()).toBe('dark');
    expect(root.hasAttribute('data-theme')).toBe(false);
  });

  it('treats a stored dark choice as the default', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'dark');
    expect(TestBed.inject(ThemeService).preference()).toBe('dark');
    expect(root.hasAttribute('data-theme')).toBe(false);
  });
});
