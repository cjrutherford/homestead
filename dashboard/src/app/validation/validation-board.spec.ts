import { describe, expect, it, beforeEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ValidationBoard } from './validation-board';

describe('ValidationBoard', () => {
  let el: HTMLElement;
  let fixture: ComponentFixture<ValidationBoard>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({ imports: [ValidationBoard] }).compileComponents();
    fixture = TestBed.createComponent(ValidationBoard);
    fixture.detectChanges();
    el = fixture.nativeElement;
  });

  it('renders one board per theme', () => {
    const themes = Array.from(el.querySelectorAll('[data-board]')).map((n) =>
      n.getAttribute('data-theme'),
    );
    expect(themes).toEqual(['light', 'dark', 'high-contrast']);
  });

  it('renders every primitive under each theme', () => {
    el.querySelectorAll('[data-board]').forEach((board) => {
      for (const selector of [
        'button.primary',
        'button:not([class])',
        'button.danger',
        'button.quiet',
        'button:disabled',
        'button[aria-busy="true"]',
        'input[type="text"]',
        'input[type="checkbox"]',
        'select',
        'textarea',
        '.field-error',
        '.badge-ok',
        '.badge-warn',
        '.badge-fail',
        '.badge-pending',
        '.badge-running',
        '.table-scroll table',
        '.card',
        'nav a',
        'pre',
        '.mono',
      ]) {
        expect(board.querySelector(selector), selector).not.toBeNull();
      }
      expect(board.querySelectorAll('app-state-message').length).toBe(5);
      expect(board.querySelector('[role="alert"]')).not.toBeNull();
      expect(board.querySelector('[role="status"]')).not.toBeNull();
    });
  });

  it('opens a sample dialog under a theme and closes it with Cancel', () => {
    const board = el.querySelector('[data-board="dark"]') as HTMLElement;
    const open = Array.from(board.querySelectorAll('button')).find((b) =>
      b.textContent?.includes('Open dialog'),
    ) as HTMLButtonElement;
    open.click();
    fixture.detectChanges();
    const dialog = board.querySelector('dialog');
    expect(dialog?.getAttribute('role')).toBe('alertdialog');
    expect(dialog?.classList.contains('dialog-danger')).toBe(true);
    (dialog?.querySelector('.quiet') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(board.querySelector('dialog')).toBeNull();
  });
});
