import { describe, expect, it, beforeEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ValidationBoard } from './validation-board';

describe('ValidationBoard', () => {
  let el: HTMLElement;

  beforeEach(async () => {
    await TestBed.configureTestingModule({ imports: [ValidationBoard] }).compileComponents();
    const fixture: ComponentFixture<ValidationBoard> = TestBed.createComponent(ValidationBoard);
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
      for (const selector of ['button', 'input', 'select', 'table', '.card', 'nav a', 'pre']) {
        expect(board.querySelector(selector), selector).not.toBeNull();
      }
      expect(board.querySelectorAll('app-state-message').length).toBe(4);
      expect(board.querySelector('[role="alert"]')).not.toBeNull();
      expect(board.querySelector('[role="status"]')).not.toBeNull();
    });
  });
});
