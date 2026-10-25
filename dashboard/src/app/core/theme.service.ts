import { DOCUMENT } from '@angular/common';
import { Injectable, inject, signal } from '@angular/core';

export type ThemePreference = 'system' | 'light' | 'dark' | 'high-contrast';

export const THEME_PREFERENCES: readonly { value: ThemePreference; label: string }[] = [
  { value: 'dark', label: 'Dark' },
  { value: 'light', label: 'Light' },
  { value: 'system', label: 'Match my system' },
  { value: 'high-contrast', label: 'High contrast' },
];

/** The theme with no stored choice (S310): dark, whatever the system says. */
export const DEFAULT_THEME: ThemePreference = 'dark';

/** localStorage key for the manual theme choice. */
export const THEME_STORAGE_KEY = 'homestead.theme';

function isPreference(value: unknown): value is ThemePreference {
  return THEME_PREFERENCES.some((option) => option.value === value);
}

/**
 * Theme choice (S282, S310). With no stored choice the dashboard is dark
 * (CSS only: :root with no data-theme is the dark theme, so the first paint
 * is dark). Light, Match my system and High contrast are persisted and
 * applied as data-theme on <html>, which the token themes in tokens.css
 * select on; choosing Dark again forgets the choice.
 */
@Injectable({ providedIn: 'root' })
export class ThemeService {
  private readonly root = inject(DOCUMENT).documentElement;

  readonly preference = signal<ThemePreference>(read());

  constructor() {
    this.apply(this.preference());
  }

  set(preference: ThemePreference): void {
    this.preference.set(preference);
    this.apply(preference);
    try {
      if (preference === DEFAULT_THEME) {
        localStorage.removeItem(THEME_STORAGE_KEY);
      } else {
        localStorage.setItem(THEME_STORAGE_KEY, preference);
      }
    } catch {
      // Private browsing: the choice holds for this tab only.
    }
  }

  private apply(preference: ThemePreference): void {
    if (preference === DEFAULT_THEME) {
      this.root.removeAttribute('data-theme');
    } else {
      this.root.setAttribute('data-theme', preference);
    }
  }
}

function read(): ThemePreference {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY);
    return isPreference(stored) ? stored : DEFAULT_THEME;
  } catch {
    return DEFAULT_THEME;
  }
}
