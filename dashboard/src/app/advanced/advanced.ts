import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ApiService, DockgeLink } from '../core/api.service';
import { StateMessage } from '../state/state-message';

/**
 * Advanced administration page (S211): the expert Compose interface
 * link with its assessed access policy. The interface grants full
 * engine control, so the page states the local-only posture outright
 * — exposure, alignment, findings, and whether the stack answers —
 * beside the link itself. This page never changes anything.
 */
@Component({
  selector: 'app-advanced',
  imports: [StateMessage],
  templateUrl: './advanced.html',
  styles: ['.advanced-text { overflow-wrap: anywhere; }'],
})
export class Advanced implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly link = signal<DockgeLink | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  protected readonly domain = signal('lan');

  ngOnInit(): void {
    this.load();
  }

  setDomain(value: string): void {
    this.domain.set(value);
  }

  /** Readable failure text; status 0 is a network failure. */
  protected describe(failure: HttpErrorResponse): string {
    if (failure.status === 0) {
      return 'Cannot reach the Homestead daemon. Check that it is running and try again.';
    }
    const body = failure.error;
    if (typeof body === 'string' && body.trim()) return body;
    if (body && typeof body.error === 'string') return body.error;
    return failure.message;
  }

  /** True when the assessed exposure is anything but local-only. */
  protected exposed(link: DockgeLink): boolean {
    return link.exposure !== 'local';
  }

  /** Only http(s) links are offered; anything else shows as text. */
  protected safeUrl(url: string): boolean {
    return /^https?:\/\//i.test(url);
  }

  load(): void {
    const domain = this.domain().trim() || 'lan';
    this.busy.set(true);
    this.error.set(null);
    this.api.dockge(domain).subscribe({
      next: (link) => {
        this.busy.set(false);
        this.link.set(link);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.link.set(null);
        this.error.set(this.describe(failure));
      },
    });
  }
}
