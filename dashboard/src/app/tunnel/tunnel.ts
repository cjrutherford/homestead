import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { ApiService, TunnelStatus } from '../core/api.service';

/**
 * Tunnel remote access page (S247): discovery with tunnel-first
 * guidance, tunnel setup, recorded routes with health, and teardown.
 * The API token seals server-side on setup and never reads back: the
 * form sends it once, status never shows it. This page never changes
 * anything except through setup and teardown.
 */
@Component({
  imports: [RouterLink],
  selector: 'app-tunnel',
  templateUrl: './tunnel.html',
})
export class Tunnel implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly status = signal<TunnelStatus | null>(null);
  protected readonly operationId = signal<number | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.tunnelStatus().subscribe({
      next: (status) => {
        this.busy.set(false);
        this.status.set(status);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  setup(
    name: string,
    hostname: string,
    service: string,
    apiToken: string,
    accountId: string,
  ): void {
    if (!name || !hostname || !service || !apiToken || !accountId) {
      this.error.set('Name, hostname, service, API token, and account ID are required.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.tunnelSetup(name, hostname, service, apiToken, accountId).subscribe({
      next: ({ id }) => {
        this.busy.set(false);
        this.operationId.set(id ?? null);
        this.notice.set('Setup started; the token is now sealed server-side.');
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  teardown(id: string, accountId: string): void {
    if (!accountId) {
      this.error.set('Teardown needs the account ID; tenancy is never guessed.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.operationId.set(null);
    this.api.tunnelTeardown(id, accountId).subscribe({
      next: ({ id: operation }) => {
        this.busy.set(false);
        this.operationId.set(operation ?? null);
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }
}
