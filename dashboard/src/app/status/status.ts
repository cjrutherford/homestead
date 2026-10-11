import { Component, inject, OnInit, signal } from '@angular/core';
import { ApiService, DoctorReport, Health, Identity } from '../core/api.service';
import { StateMessage } from '../state/state-message';

/** Appliance status page: daemon health, claim state, diagnostics (S044). */
@Component({
  selector: 'app-status',
  imports: [StateMessage],
  templateUrl: './status.html',
})
export class Status implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly health = signal<Health | null>(null);
  protected readonly identity = signal<Identity | null>(null);
  protected readonly report = signal<DoctorReport | null>(null);
  protected readonly error = signal<string | null>(null);

  ngOnInit(): void {
    this.api.health().subscribe({
      next: (health) => this.health.set(health),
      error: () => this.unreachable(),
    });
    this.api.identity().subscribe({
      next: (identity) => this.identity.set(identity),
      error: () => this.unreachable(),
    });
    this.api.doctor().subscribe({
      next: (report) => this.report.set(report),
      error: () => this.unreachable(),
    });
  }

  private unreachable(): void {
    this.error.set('Daemon unreachable. Is homestead-server running?');
  }
}
