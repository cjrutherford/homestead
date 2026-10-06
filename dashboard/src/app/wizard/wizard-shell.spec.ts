import { describe, expect, it, beforeEach } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { WizardShell } from './wizard-shell';

describe('WizardShell', () => {
  let fixture: ComponentFixture<WizardShell>;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [WizardShell],
    }).compileComponents();
    fixture = TestBed.createComponent(WizardShell);
    fixture.componentRef.setInput('steps', ['First', 'Second', 'Done']);
    fixture.componentRef.setInput('current', 1);
    fixture.detectChanges();
  });

  it('shows step progress', () => {
    const text = fixture.nativeElement.textContent as string;
    expect(text).toContain('Step 2 of 3: Second');
  });

  it('marks the current step', () => {
    const items = fixture.nativeElement.querySelectorAll('li');
    expect(items.length).toBe(3);
    expect(items[1].getAttribute('aria-current')).toBe('step');
    expect(items[0].getAttribute('aria-current')).toBeNull();
  });

  it('emits back except on the first step', () => {
    let backed = false;
    fixture.componentInstance.back.subscribe(() => {
      backed = true;
    });
    (fixture.nativeElement.querySelector('button') as HTMLElement).click();
    expect(backed).toBe(true);
  });

  it('hides back on the first step', () => {
    fixture.componentRef.setInput('current', 0);
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('button')).toBeNull();
  });
});
