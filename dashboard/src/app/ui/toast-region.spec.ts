import { describe, expect, it } from 'vitest';
import { TestBed } from '@angular/core/testing';
import { ToastService } from '../core/toast.service';
import { ToastRegion } from './toast-region';

describe('ToastRegion', () => {
  it('has a polite live region and lists toasts that can be dismissed', () => {
    const fixture = TestBed.createComponent(ToastRegion);
    const toasts = TestBed.inject(ToastService);
    fixture.detectChanges();
    const root = fixture.nativeElement as HTMLElement;
    const live = root.querySelector('[aria-live="polite"]')!;
    expect(live).not.toBeNull();
    toasts.show('Routes reconciled');
    fixture.detectChanges();
    expect(live.textContent).toContain('Routes reconciled');
    expect(root.querySelector('.toast')?.textContent).toContain('Routes reconciled');
    root.querySelector<HTMLButtonElement>('.toast-dismiss')!.click();
    fixture.detectChanges();
    expect(root.querySelector('.toast')).toBeNull();
  });
});
