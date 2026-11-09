import { Component, inject, input } from '@angular/core';
import { ToastService } from '../core/toast.service';

/**
 * A command or identifier shown as code with a Copy button (S284). The
 * clipboard is only a convenience: the text stays selectable, and a
 * browser that refuses clipboard access leaves the code on screen.
 */
@Component({
  selector: 'app-copy-code',
  template: `
    <span class="copy-code">
      <code>{{ text() }}</code>
      <button type="button" class="quiet" (click)="copy()" [attr.aria-label]="'Copy ' + text()">Copy</button>
    </span>
  `,
})
export class CopyCode {
  readonly text = input.required<string>();
  private readonly toasts = inject(ToastService);

  protected copy(): void {
    const clipboard = typeof navigator === 'undefined' ? undefined : navigator.clipboard;
    if (!clipboard) {
      this.toasts.show('Copy is not available here. Select the command and copy it.', 'notice');
      return;
    }
    clipboard.writeText(this.text()).then(
      () => this.toasts.show('Command copied'),
      () => this.toasts.show('Copy was refused. Select the command and copy it.', 'notice'),
    );
  }
}
