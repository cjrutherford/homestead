import {
  AfterViewInit,
  Component,
  ElementRef,
  OnDestroy,
  input,
  output,
  viewChild,
} from '@angular/core';

let nextId = 0;

/** How long the exit transition runs; matches --duration-fast. */
export const DIALOG_EXIT_MS = 120;

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Modal dialog primitive (S282) on the native <dialog> element, opened with
 * showModal(). Focus moves into the dialog on open, Tab stays inside it,
 * Escape dismisses, and focus returns to the opener on close. The dialog
 * is an alertdialog: the page keeps the confirmation state, renders this
 * component while it is pending, and removes it on confirm or cancel.
 *
 * Content is projected: the body is the default slot and buttons go in an
 * element with class "dialog-actions". `variant="danger"` marks a
 * destructive confirmation (brick accent); give its confirm button
 * class="danger".
 */
@Component({
  selector: 'app-dialog',
  template: `
    <dialog
      #dialog
      class="dialog"
      [class.dialog-danger]="variant() === 'danger'"
      role="alertdialog"
      aria-modal="true"
      tabindex="-1"
      [attr.aria-labelledby]="titleId"
      [attr.aria-describedby]="bodyId"
      (cancel)="dismiss($event)"
      (keydown.escape)="dismiss($event)"
      (keydown.tab)="trap($event)"
      (keydown.shift.tab)="trap($event)"
    >
      <h2 class="dialog-title" [id]="titleId">{{ heading() }}</h2>
      <div class="dialog-body" [id]="bodyId"><ng-content /></div>
      <ng-content select=".dialog-actions" />
    </dialog>
  `,
})
export class Dialog implements AfterViewInit, OnDestroy {
  readonly heading = input.required<string>();
  readonly variant = input<'confirm' | 'danger'>('confirm');
  /** Emits when the owner presses Escape. The page decides what that means. */
  readonly dismissed = output<void>();

  protected readonly titleId = `app-dialog-title-${nextId}`;
  protected readonly bodyId = `app-dialog-body-${nextId++}`;

  private readonly dialog = viewChild.required<ElementRef<HTMLDialogElement>>('dialog');
  private opener: HTMLElement | null = null;

  ngAfterViewInit(): void {
    const active = document.activeElement;
    this.opener = active instanceof HTMLElement && active !== document.body ? active : null;
    const dialog = this.dialog().nativeElement;
    if (typeof dialog.showModal === 'function') {
      dialog.showModal();
    } else {
      dialog.setAttribute('open', '');
    }
    // The dialog itself takes focus so its heading and text are read first.
    dialog.focus();
  }

  ngOnDestroy(): void {
    const dialog = this.dialog().nativeElement;
    this.leave(dialog);
    if (typeof dialog.close === 'function' && dialog.open) {
      dialog.close();
    }
    const opener = this.opener;
    setTimeout(() => {
      if (opener?.isConnected) {
        opener.focus();
      }
    });
  }

  /**
   * Exit transition. The page removes this component at once, so a
   * non-modal copy of the dialog and a fading scrim stay behind for
   * DIALOG_EXIT_MS and are then removed. Skipped under reduced motion.
   */
  private leave(dialog: HTMLDialogElement): void {
    if (!dialog.open || typeof matchMedia !== 'function' || matchMedia('(prefers-reduced-motion: reduce)').matches) {
      return;
    }
    const ghost = dialog.cloneNode(true) as HTMLDialogElement;
    ghost.removeAttribute('id');
    ghost.classList.add('dialog-ghost');
    ghost.setAttribute('inert', '');
    ghost.setAttribute('aria-hidden', 'true');
    const scrim = document.createElement('div');
    scrim.className = 'dialog-ghost-scrim';
    scrim.setAttribute('aria-hidden', 'true');
    document.body.append(scrim, ghost);
    setTimeout(() => {
      scrim.remove();
      ghost.remove();
    }, DIALOG_EXIT_MS);
  }

  protected dismiss(event: Event): void {
    event.preventDefault();
    this.dismissed.emit();
  }

  /** Keeps Tab and Shift+Tab inside the dialog (native modals also inert the page). */
  protected trap(event: Event): void {
    const dialog = this.dialog().nativeElement;
    const items = Array.from(dialog.querySelectorAll<HTMLElement>(FOCUSABLE));
    if (items.length === 0) {
      event.preventDefault();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    const backwards = (event as KeyboardEvent).shiftKey;
    if (backwards && (active === first || active === dialog)) {
      event.preventDefault();
      last.focus();
    } else if (!backwards && active === last) {
      event.preventDefault();
      first.focus();
    }
  }
}
