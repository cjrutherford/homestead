import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TOAST_ERROR_MS, TOAST_MS, ToastService } from './toast.service';

describe('ToastService', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('shows a toast, announces it, and removes it after a while', () => {
    const toasts = new ToastService();
    toasts.show('Routes reconciled');
    expect(toasts.toasts().map((t) => t.message)).toEqual(['Routes reconciled']);
    expect(toasts.announcement().trim()).toBe('Routes reconciled');
    vi.advanceTimersByTime(TOAST_MS + 1);
    expect(toasts.toasts()).toEqual([]);
  });

  it('keeps errors longer and lets the owner dismiss early', () => {
    const toasts = new ToastService();
    const id = toasts.show('Install failed.', 'error');
    vi.advanceTimersByTime(TOAST_MS + 1);
    expect(toasts.toasts().length).toBe(1);
    toasts.dismiss(id);
    expect(toasts.toasts()).toEqual([]);
    toasts.show('Again', 'error');
    vi.advanceTimersByTime(TOAST_ERROR_MS + 1);
    expect(toasts.toasts()).toEqual([]);
  });

  it('announces without a toast, and a repeat still changes the live text', () => {
    const toasts = new ToastService();
    toasts.announce('Step 2 of 3');
    const first = toasts.announcement();
    toasts.announce('Step 2 of 3');
    expect(toasts.toasts()).toEqual([]);
    expect(toasts.announcement()).not.toBe(first);
    expect(toasts.announcement().trim()).toBe('Step 2 of 3');
  });
});
