import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { ApiService, CraftApp } from '../core/api.service';
import { RouterLink } from '@angular/router';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';

/**
 * Craft apps start page (S281, ADR-031): the installed craft apps with
 * their local HTTPS addresses, derived by the daemon from the catalog and
 * the edge route table. Matches `homesteadctl craft apps`. The page makes
 * no external call and loads no third-party asset; installing or removing
 * an app shows on the next load.
 */
@Component({
  imports: [Loading, StateMessage, RouterLink],
  selector: 'app-craft-apps',
  templateUrl: './craft-apps.html',
})
export class CraftApps implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly apps = signal<CraftApp[]>([]);
  protected readonly loading = signal(true);
  protected readonly error = signal<string | null>(null);

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading.set(true);
    this.error.set(null);
    this.api.craftApps().subscribe({
      next: (result) => {
        this.apps.set(result.apps);
        this.loading.set(false);
      },
      error: (failure: HttpErrorResponse) => {
        this.loading.set(false);
        this.error.set(this.describe(failure));
      },
    });
  }

  protected badgeFor(status: string): string {
    return status === 'installed' || status === 'running' ? 'badge-ok' : '';
  }

  private describe(failure: HttpErrorResponse): string {
    if (failure.status === 0) {
      return "Can't reach the appliance. Check that homestead-server is running.";
    }
    const body = failure.error;
    if (typeof body === 'string' && body.trim()) {
      return body.trim();
    }
    return failure.message;
  }
}
