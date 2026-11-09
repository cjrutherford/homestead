import { HttpErrorResponse } from '@angular/common/http';
import { Component, computed, inject, OnInit, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { ApiService, DoctorCheck, DoctorReport } from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import { CopyCode } from '../ui/copy-code';

/** The groups in the order the owner reads them (S316). */
const GROUPS = ['System', 'Storage', 'Network', 'Certificates', 'Tunnel', 'Sign-in', 'Apps'];

export interface DoctorGroup {
  name: string;
  checks: DoctorCheck[];
  problems: DoctorCheck[];
  good: DoctorCheck[];
  state: 'ok' | 'warn' | 'fail';
}

/**
 * Doctor (S316): every check, grouped, with what it means and what to do.
 * It reads and explains; it never acts. "Check again" also runs the live
 * probes (tunnel, Authentik, apps), which can take a few seconds.
 */
@Component({
  selector: 'app-doctor',
  imports: [StateMessage, Loading, CopyCode, RouterLink],
  templateUrl: './doctor.html',
})
export class Doctor implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly report = signal<DoctorReport | null>(null);
  /** The daemon could not be read at all. */
  protected readonly error = signal<string | null>(null);
  /** A live run failed; the last report stays on screen. */
  protected readonly probeError = signal<string | null>(null);
  protected readonly loading = signal(true);
  protected readonly running = signal(false);

  protected readonly checks = computed(() => this.report()?.checks ?? []);
  protected readonly problems = computed(() =>
    this.checks()
      .filter((check) => check.status === 'warn' || check.status === 'fail')
      .sort((a, b) => Number(b.status === 'fail') - Number(a.status === 'fail')),
  );
  protected readonly failing = computed(() => this.problems().filter((check) => check.status === 'fail').length);
  protected readonly unknowns = computed(() => this.checks().filter((check) => check.status === 'unknown').length);

  protected readonly groups = computed<DoctorGroup[]>(() => {
    const byName = new Map<string, DoctorCheck[]>();
    for (const check of this.checks()) {
      const name = check.group || 'System';
      byName.set(name, [...(byName.get(name) ?? []), check]);
    }
    const names = [...GROUPS.filter((name) => byName.has(name)), ...[...byName.keys()].filter((name) => !GROUPS.includes(name))];
    return names.map((name) => {
      const checks = byName.get(name) ?? [];
      const problems = checks.filter((check) => check.status === 'warn' || check.status === 'fail');
      return {
        name,
        checks,
        problems,
        good: checks.filter((check) => check.status === 'ok'),
        state: problems.some((check) => check.status === 'fail') ? 'fail' : problems.length ? 'warn' : 'ok',
      };
    });
  });

  /** The masthead sentence: the overall state in words. */
  protected readonly lead = computed(() => {
    if (this.error()) {
      return 'Doctor cannot read the appliance.';
    }
    if (!this.report()) {
      return 'Checking the appliance.';
    }
    const count = this.problems().length;
    if (count === 1) {
      return '1 problem needs you.';
    }
    if (count > 1) {
      return `${count} problems need you.`;
    }
    return 'Everything checked is fine.';
  });

  protected readonly checkedAt = computed(() => {
    const at = this.report()?.checked_at;
    return at ? new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : null;
  });

  ngOnInit(): void {
    this.api.doctor().subscribe({
      next: (report) => {
        this.report.set(report);
        this.loading.set(false);
      },
      error: (err: HttpErrorResponse) => {
        this.error.set(
          err.status === 401 || err.status === 403
            ? 'Sign in as the owner to see diagnostics.'
            : "Can't read the diagnostics. Check that homestead-server is running.",
        );
        this.loading.set(false);
      },
    });
  }

  /** Runs the live probes. A failure keeps the report already on screen. */
  protected checkAgain(): void {
    if (this.running()) {
      return;
    }
    this.running.set(true);
    this.probeError.set(null);
    this.api.doctor(true).subscribe({
      next: (report) => {
        this.report.set(report);
        this.error.set(null);
        this.running.set(false);
      },
      error: () => {
        this.probeError.set('The live checks did not finish. The checks below are from before; try again in a moment.');
        this.running.set(false);
      },
    });
  }

  protected titleFor(check: DoctorCheck): string {
    return check.title || check.name;
  }

  protected badgeFor(status: string): string {
    return status === 'ok' ? 'badge-ok' : status === 'warn' ? 'badge-warn' : status === 'fail' ? 'badge-fail' : '';
  }
}
