import { describe, expect, it } from 'vitest';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ReauthRequest } from '../core/reauth';
import { ReauthDialog } from './reauth-dialog';

describe('ReauthDialog', () => {
  async function open(invalid = false): Promise<{ fixture: ComponentFixture<ReauthDialog>; answers: (string | null)[] }> {
    await TestBed.configureTestingModule({ imports: [ReauthDialog] }).compileComponents();
    const answers: (string | null)[] = [];
    const request: ReauthRequest = { invalid, answer: (p) => answers.push(p) };
    const fixture = TestBed.createComponent(ReauthDialog);
    fixture.componentRef.setInput('request', request);
    document.body.appendChild(fixture.nativeElement);
    fixture.detectChanges();
    return { fixture, answers };
  }

  it('asks for the local password and explains why', async () => {
    const { fixture } = await open();
    const dialog = fixture.nativeElement.querySelector('dialog') as HTMLDialogElement;
    expect(dialog.textContent).toContain('Confirm with your local password');
    expect(dialog.textContent).toContain('sent once');
    expect(dialog.querySelector('input[type="password"]')).not.toBeNull();
    expect(dialog.textContent).not.toContain('not right');
    fixture.nativeElement.remove();
  });

  it('says so when the last password was wrong', async () => {
    const { fixture } = await open(true);
    expect((fixture.nativeElement as HTMLElement).textContent).toContain('That password is not right');
    fixture.nativeElement.remove();
  });

  it('answers with the typed password and ignores an empty one', async () => {
    const { fixture, answers } = await open();
    const el = fixture.nativeElement as HTMLElement;
    const input = el.querySelector('input') as HTMLInputElement;
    const confirm = el.querySelector('.dialog-actions button') as HTMLButtonElement;
    confirm.click();
    expect(answers).toEqual([]);
    input.value = 'owner-password';
    confirm.click();
    expect(answers).toEqual(['owner-password']);
    el.remove();
  });

  it('answers null on Cancel and on Escape', async () => {
    const { fixture, answers } = await open();
    const el = fixture.nativeElement as HTMLElement;
    (el.querySelector('.dialog-actions .quiet') as HTMLButtonElement).click();
    el.querySelector('dialog')?.dispatchEvent(new Event('cancel', { cancelable: true }));
    expect(answers).toEqual([null, null]);
    el.remove();
  });
});
