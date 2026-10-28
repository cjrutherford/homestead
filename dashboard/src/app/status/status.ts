import { HttpErrorResponse } from '@angular/common/http';
import { TitleCasePipe } from '@angular/common';
import { Component, computed, inject, OnInit, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { ApiService, DoctorCheck, DoctorReport, Health, Identity } from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import { CopyCode } from '../ui/copy-code';

/** What the owner can do about a check, in plain words (the daemon only reports the symptom). */
export interface Fix {
  text: string;
  command?: string;
  /** A page in the dashboard where the fix is made. */
  link?: { path: string; label: string };
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
  'stale-mounts': {
    text: 'Reconnect the drive, or remove the mount on the Storage page.',
    link: { path: '/storage', label: 'Open Storage' },
  },
  'state-dirs': { text: 'Re-run the installer to recreate the Homestead folders.', command: 'sudo make install' },
};

/** Button words for the pages a daemon fix links to. */
const PAGE_LABELS: Record<string, string> = {
  '/catalog': 'Open the catalog',
  '/doctor': 'Open Doctor',
  '/people': 'Open People',
  '/tunnel': 'Open Tunnel',
  '/edge': 'Open the Edge page',
};

const DEFAULT_FIX: Fix = { text: 'Open Doctor for what this means and how to fix it.', link: { path: '/doctor', label: 'Open Doctor' } };

/** Appliance status page: daemon health, claim state, diagnostics (S044). */
@Component({
  selector: 'app-status',
  imports: [StateMessage, Loading, CopyCode, TitleCasePipe, RouterLink],
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
      .filter((check) => check.status === 'warn' || check.status === 'fail')
      .sort((a, b) => Number(b.status === 'fail') - Number(a.status === 'fail')),
  );
  protected readonly good = computed(() => (this.report()?.checks ?? []).filter((check) => check.status === 'ok'));

  /** The furrow of the Needs you field: its worst item. */
  protected readonly worst = computed(() => (this.problems().some((c) => c.status === 'fail') ? 'fail' : 'warn'));

  /** The masthead sentence: the appliance's most important fact, from what the page has read. */
  protected readonly fact = computed(() => {
    if (this.error()) {
      return 'Homestead cannot be reached from this browser.';
    }
    const health = this.health();
    if (!health && !this.identity()) {
      return 'Checking the appliance.';
    }
    const running = health ? 'Homestead is running.' : 'Homestead answers.';
    if (this.unclaimed()) {
      return `${running} It is not claimed yet.`;
    }
    const problems = this.problems().length;
    if (problems) {
      return `${running} ${problems === 1 ? '1 check needs you.' : `${problems} checks need you.`}`;
    }
    return this.report() ? `${running} Nothing needs you.` : running;
  });

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

  /** The check's own title from the daemon (S316) wins over the name; known names have a label here. */
  protected labelFor(check: DoctorCheck): string {
    return LABELS[check.name] ?? check.title ?? check.name;
  }

  /**
   * What to do: the words here for known checks, else the daemon's own fix
   * (S324: an unfinished app setup names the catalog), else Doctor. A fix
   * with a page links there; a command only is copied.
   */
  protected fixFor(check: DoctorCheck): Fix {
    const known = FIXES[check.name];
    if (known) {
      return known;
    }
    const fix = check.fix;
    if (!fix?.text) {
      return DEFAULT_FIX;
    }
    if (fix.link) {
      return { text: fix.text, link: { path: fix.link, label: PAGE_LABELS[fix.link] ?? 'Open the page' } };
    }
    return { text: fix.text, command: fix.command };
  }

  private unreachable(): void {
    this.error.set("Can't reach the appliance. Check that homestead-server is running.");
  }
}
