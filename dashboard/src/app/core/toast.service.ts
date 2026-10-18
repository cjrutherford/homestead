import { Injectable, signal } from '@angular/core';

export type ToastKind = 'status' | 'error' | 'notice';

export interface Toast {
  id: number;
  kind: ToastKind;
  message: string;
}

/** Visible time of a toast; errors stay longer. */
export const TOAST_MS = 6000;
export const TOAST_ERROR_MS = 12000;

/**
 * Feedback for completed actions (S283). `show` adds a short-lived toast and
 * announces it politely; `announce` speaks without showing anything (step
 * progress). The region component renders both.
 */
@Injectable({ providedIn: 'root' })
export class ToastService {
  readonly toasts = signal<Toast[]>([]);
  /** Text of the polite live region. */
  readonly announcement = signal('');

  private nextId = 1;
  private flip = false;
  private readonly timers = new Map<number, ReturnType<typeof setTimeout>>();

  show(message: string, kind: ToastKind = 'status'): number {
    const id = this.nextId++;
    this.toasts.update((list) => [...list, { id, kind, message }].slice(-4));
    this.announce(message);
    this.timers.set(
      id,
      setTimeout(() => this.dismiss(id), kind === 'error' ? TOAST_ERROR_MS : TOAST_MS),
    );
    return id;
  }

  dismiss(id: number): void {
    const timer = this.timers.get(id);
    if (timer !== undefined) {
      clearTimeout(timer);
      this.timers.delete(id);
    }
    this.toasts.update((list) => list.filter((toast) => toast.id !== id));
  }

  /** Polite announcement only. A repeated message still reads again. */
  announce(message: string): void {
    this.flip = !this.flip;
    this.announcement.set(this.flip ? message : `${message} `);
  }
}
