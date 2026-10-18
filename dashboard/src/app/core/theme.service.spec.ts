import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { THEME_STORAGE_KEY, ThemeService } from './theme.service';

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

  it('follows the system by default: no data-theme attribute', () => {
    const service = TestBed.inject(ThemeService);
    expect(service.preference()).toBe('system');
    expect(root.hasAttribute('data-theme')).toBe(false);
  });

  it('applies and persists a manual choice', () => {
    const service = TestBed.inject(ThemeService);
    for (const choice of ['light', 'dark', 'high-contrast'] as const) {
      service.set(choice);
      expect(root.getAttribute('data-theme')).toBe(choice);
      expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe(choice);
    }
  });

  it('returns to the system theme and forgets the choice', () => {
    const service = TestBed.inject(ThemeService);
    service.set('dark');
    service.set('system');
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
    expect(TestBed.inject(ThemeService).preference()).toBe('system');
    expect(root.hasAttribute('data-theme')).toBe(false);
  });
});
