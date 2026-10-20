import { HttpErrorResponse } from '@angular/common/http';
import { TitleCasePipe } from '@angular/common';
import { Component, computed, inject, OnInit, signal } from '@angular/core';
import { ApiService, DoctorReport, Health, Identity } from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import { CopyCode } from '../ui/copy-code';

/** What the owner can do about a check, in plain words (the daemon only reports the symptom). */
export interface Fix {
  text: string;
  command?: string;
}

const LABELS: Record<string, string> = {
  discovery: 'Host details',
  hardware: 'Hardware',
  network: 'Network',
  storage: 'Storage',
  'os-distro': 'Operating system',
  memory: 'Memory',
  cpu: 'Processor',
  'default-gateway': 'Internet route',
  'dns-resolvers': 'Name lookup',
  'root-disk': 'System disk space',
  'stale-mounts': 'Storage mounts',
  'state-dirs': 'Homestead folders',
};

const FIXES: Record<string, Fix> = {
  discovery: { text: 'Restart the service, then check its log for the error above.', command: 'sudo systemctl restart homestead-server' },
  hardware: { text: 'Restart the service, then check its log for the error above.', command: 'sudo systemctl restart homestead-server' },
  network: { text: 'Restart the service, then check its log for the error above.', command: 'sudo systemctl restart homestead-server' },
  storage: { text: 'Restart the service, then check its log for the error above.', command: 'sudo systemctl restart homestead-server' },
  'os-distro': { text: 'Homestead is built for Debian or Ubuntu Server. Move to one of those to get support.' },
  memory: { text: 'Add memory, or stop apps you do not use. Below 1 GiB the appliance cannot run reliably.' },
  cpu: { text: 'It works, but apps will feel slow. Use a machine with at least two cores.' },
  'default-gateway': { text: 'Connect the appliance to your router. Until then it works on your network only, with no updates or remote access.' },
  'dns-resolvers': { text: 'Set a name server on the appliance, or in your router. Updates and the catalog need it.' },
  'root-disk': { text: 'Free space on the system disk. Remove old images and backups you no longer need.', command: 'docker system df' },
  'stale-mounts': { text: 'Reconnect the drive, or remove the mount on the Storage page.' },
  'state-dirs': { text: 'Re-run the installer to recreate the Homestead folders.', command: 'sudo make install' },
};

const DEFAULT_FIX: Fix = { text: 'Run the check from a terminal for more detail.', command: 'homesteadctl doctor' };

/** Appliance status page: daemon health, claim state, diagnostics (S044). */
@Component({
  selector: 'app-status',
  imports: [StateMessage, Loading, CopyCode, TitleCasePipe],
  templateUrl: './status.html',
})
export class Status implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly health = signal<Health | null>(null);
  protected readonly identity = signal<Identity | null>(null);
  protected readonly report = signal<DoctorReport | null>(null);
  protected readonly error = signal<string | null>(null);
  /** The appliance answers but is not claimed: diagnostics need a claim first (S292). */
  protected readonly unclaimed = computed(() => {
    const state = this.identity()?.state;
    return state !== undefined && state !== 'CLAIMED';
  });

  /** Checks that are not ok, worst first. */
  protected readonly problems = computed(() =>
    (this.report()?.checks ?? [])
      .filter((check) => check.status !== 'ok')
      .sort((a, b) => Number(b.status === 'fail') - Number(a.status === 'fail')),
  );
  protected readonly good = computed(() => (this.report()?.checks ?? []).filter((check) => check.status === 'ok'));

  ngOnInit(): void {
    this.load();
  }

  protected load(): void {
    this.error.set(null);
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
      // Diagnostics are for the owner: an unclaimed appliance refuses them,
      // which is not the same as being unreachable.
      error: (err: HttpErrorResponse) => {
        if (err.status !== 401 && err.status !== 403) {
          this.unreachable();
        }
      },
    });
  }

  /** Badge colour and glyph for a doctor check status; unknown statuses stay neutral. */
  protected badgeFor(status: string): string {
    switch (status) {
      case 'ok':
        return 'badge-ok';
      case 'warn':
        return 'badge-warn';
      case 'fail':
        return 'badge-fail';
      default:
        return '';
    }
  }

  protected labelFor(name: string): string {
    return LABELS[name] ?? name;
  }

  protected fixFor(name: string): Fix {
    return FIXES[name] ?? DEFAULT_FIX;
  }

  private unreachable(): void {
    this.error.set("Can't reach the appliance. Check that homestead-server is running.");
  }
}
