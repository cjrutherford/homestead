import { afterEach, describe, expect, it } from 'vitest';
import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Dialog } from './dialog';

@Component({
  imports: [Dialog],
  template: `
    <button id="opener" type="button" (click)="open.set(true)">Open</button>
    @if (open()) {
      <app-dialog heading="Remove it?" [variant]="variant()" (dismissed)="open.set(false)">
        <p>Body text</p>
        <div class="dialog-actions">
          <button id="confirm" type="button" class="danger">Confirm</button>
          <button id="cancel" type="button" (click)="open.set(false)">Cancel</button>
        </div>
      </app-dialog>
    }
  `,
})
class Host {
  readonly open = signal(false);
  readonly variant = signal<'confirm' | 'danger'>('danger');
}

describe('Dialog', () => {
  let fixture: ComponentFixture<Host>;

  async function openDialog(): Promise<{ opener: HTMLElement; dialog: HTMLElement }> {
    await TestBed.configureTestingModule({ imports: [Host] }).compileComponents();
    fixture = TestBed.createComponent(Host);
    document.body.appendChild(fixture.nativeElement);
    fixture.detectChanges();
    const opener = fixture.nativeElement.querySelector('#opener') as HTMLElement;
    opener.focus();
    opener.click();
    fixture.detectChanges();
    return { opener, dialog: fixture.nativeElement.querySelector('dialog') as HTMLElement };
  }

  afterEach(() => fixture?.nativeElement.remove());

  it('is a modal alertdialog on a native dialog element with a heading and body', async () => {
    const { dialog } = await openDialog();
    expect(dialog.tagName).toBe('DIALOG');
    expect(dialog.getAttribute('role')).toBe('alertdialog');
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(dialog.hasAttribute('open')).toBe(true);
    const title = dialog.querySelector('h2') as HTMLElement;
    expect(title.textContent).toContain('Remove it?');
    expect(dialog.getAttribute('aria-labelledby')).toBe(title.id);
    expect(document.getElementById(dialog.getAttribute('aria-describedby') ?? '')?.textContent).toContain(
      'Body text',
    );
  });

  it('marks the danger variant', async () => {
    const { dialog } = await openDialog();
    expect(dialog.classList.contains('dialog-danger')).toBe(true);
    fixture.componentInstance.variant.set('confirm');
    fixture.detectChanges();
    expect(dialog.classList.contains('dialog-danger')).toBe(false);
  });

  it('moves focus into the dialog, and Escape dismisses and returns focus to the opener', async () => {
    const { opener, dialog } = await openDialog();
    expect(document.activeElement).toBe(dialog);
    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('dialog')).toBeNull();
    await new Promise((resolve) => setTimeout(resolve));
    expect(document.activeElement).toBe(opener);
  });

  it('treats the native cancel event like Escape', async () => {
    const { dialog } = await openDialog();
    const cancel = new Event('cancel', { cancelable: true });
    dialog.dispatchEvent(cancel);
    fixture.detectChanges();
    expect(cancel.defaultPrevented).toBe(true);
    expect(fixture.nativeElement.querySelector('dialog')).toBeNull();
  });

  it('traps Tab and Shift+Tab inside the dialog', async () => {
    const { dialog } = await openDialog();
    const confirm = dialog.querySelector('#confirm') as HTMLElement;
    const cancel = dialog.querySelector('#cancel') as HTMLElement;
    cancel.focus();
    const forward = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    cancel.dispatchEvent(forward);
    expect(forward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(confirm);
    const backward = new KeyboardEvent('keydown', {
      key: 'Tab',
      shiftKey: true,
      bubbles: true,
      cancelable: true,
    });
    confirm.dispatchEvent(backward);
    expect(backward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(cancel);
  });

  it('returns focus to the opener when closed from a button', async () => {
    const { opener, dialog } = await openDialog();
    (dialog.querySelector('#cancel') as HTMLElement).click();
    fixture.detectChanges();
    await new Promise((resolve) => setTimeout(resolve));
    expect(document.activeElement).toBe(opener);
  });
});
