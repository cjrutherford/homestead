import { Directive, ElementRef, inject, OnDestroy, OnInit, output } from '@angular/core';

/**
 * Focus management for inline confirmation dialogs: focus moves into the
 * dialog when it opens, Escape dismisses it, and focus returns to the
 * element that was focused before it opened.
 */
@Directive({
  selector: '[appDialogFocus]',
  host: {
    tabindex: '-1',
    '(keydown.escape)': 'dismiss.emit()',
  },
})
export class DialogFocus implements OnInit, OnDestroy {
  private readonly host = inject<ElementRef<HTMLElement>>(ElementRef);
  private opener: HTMLElement | null = null;
  readonly dismiss = output<void>();

  ngOnInit(): void {
    const active = document.activeElement;
    this.opener = active instanceof HTMLElement && active !== document.body ? active : null;
    // Defer so the element is attached and the opener's disabled state settled.
    queueMicrotask(() => this.host.nativeElement.focus());
  }

  ngOnDestroy(): void {
    const opener = this.opener;
    setTimeout(() => {
      if (opener?.isConnected) {
        opener.focus();
      }
    });
  }
}
