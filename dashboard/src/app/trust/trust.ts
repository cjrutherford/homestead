import { WhenPipe } from '../core/format-time';
import { Component, computed, inject, OnDestroy, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { Subscription } from 'rxjs';
import { FormsModule } from '@angular/forms';
import {
  ApiService,
  DashboardAddress,
  LocalCaReport,
  LocalCaRotationPlan,
  OperationState,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { CopyCode } from '../ui/copy-code';
import { Dialog } from '../ui/dialog';
import { ariaSort, nextSort, SortState, sortRows } from '../core/sort';
import { Loading } from '../ui/loading';
import { OperationProgress } from '../ui/operation-progress';
import { SessionService } from '../auth/session.service';
import { ApprovedPlan, approvedPlan, Permit } from '../ui/permit';

const TERMINAL = new Set(['succeeded', 'failed', 'cancelled']);

export interface TrustGuide {
  id: string;
  title: string;
  /** Which download the platform wants. */
  file: 'pem' | 'der';
  steps: string[];
}

/**
 * Per-OS and per-browser install steps (S280). The same text lives in
 * docs/architecture/INTERNAL_CA_TRUST.md. Homestead never installs the root for
 * you: you do it, deliberately, on each device.
 */
export const TRUST_GUIDES: TrustGuide[] = [
  {
    id: 'windows',
    title: 'Windows',
    file: 'der',
    steps: [
      'Download the .crt file and double-click it.',
      'Choose Install Certificate, Local Machine (or Current User), then "Place all certificates in the following store" and pick "Trusted Root Certification Authorities".',
      'Finish the wizard and accept the prompt. Restart the browser.',
      'To remove it later: run certmgr.msc, open Trusted Root Certification Authorities, and delete the Homestead Local CA entry.',
    ],
  },
  {
    id: 'macos',
    title: 'macOS',
    file: 'der',
    steps: [
      'Double-click the .crt file; Keychain Access opens. Add it to the System keychain.',
      'Open the certificate, expand Trust, and set "When using this certificate" to Always Trust. Authenticate to confirm.',
      'Command line: sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain homestead-local-ca.crt',
      'To remove it later: delete the entry in Keychain Access, or sudo security delete-certificate -c "Homestead Local CA".',
    ],
  },
  {
    id: 'ios',
    title: 'iPhone and iPad',
    file: 'der',
    steps: [
      'Open the download link in Safari and allow the configuration profile download.',
      'Open Settings, tap "Profile Downloaded", and install the profile.',
      'Then open Settings, General, About, Certificate Trust Settings and switch on full trust for Homestead Local CA. Without this step the root is installed but not trusted for websites.',
      'To remove it later: Settings, General, VPN & Device Management, delete the profile.',
    ],
  },
  {
    id: 'android',
    title: 'Android',
    file: 'der',
    steps: [
      'Download the .crt file. Open Settings, Security (or Security & privacy), More security settings, Encryption & credentials, Install a certificate, CA certificate. Confirm the warning.',
      'Chrome and the system browser trust user-installed CAs. Many apps do not: apps targeting recent Android versions ignore the user store unless they opt in.',
      'To remove it later: Encryption & credentials, User credentials, remove the certificate.',
    ],
  },
  {
    id: 'linux',
    title: 'Linux (system store)',
    file: 'pem',
    steps: [
      'Debian and Ubuntu: sudo cp homestead-local-ca.pem /usr/local/share/ca-certificates/homestead-local-ca.crt && sudo update-ca-certificates',
      'Fedora and RHEL: sudo cp homestead-local-ca.pem /etc/pki/ca-trust/source/anchors/ && sudo update-ca-trust',
      'Arch: sudo trust anchor homestead-local-ca.pem',
      'To remove it later: delete the copied file and run the same update command.',
    ],
  },
  {
    id: 'firefox',
    title: 'Firefox (all platforms)',
    file: 'pem',
    steps: [
      'Firefox keeps its own store. Open Settings, Privacy & Security, View Certificates, Authorities, Import.',
      'Select the downloaded file and tick "Trust this CA to identify websites".',
      'To remove it later: the same dialog, select the certificate, Delete or Distrust.',
    ],
  },
  {
    id: 'chrome-linux',
    title: 'Chrome and Chromium on Linux',
    file: 'pem',
    steps: [
      'Chrome on Linux uses the per-user NSS database, not the system store. Install libnss3-tools, then run: certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n "Homestead Local CA" -i homestead-local-ca.pem',
      'Or in Chrome: Settings, Privacy and security, Security, Manage certificates, Authorities, Import.',
      'To remove it later: certutil -d sql:$HOME/.pki/nssdb -D -n "Homestead Local CA"',
    ],
  },
];

/**
 * Trust this appliance (S280, ADR-030): the internal CA's SHA-256
 * fingerprint, the public root downloads, per-OS install steps, local
 * leaf status, and previewed root rotation. Matches
 * `homesteadctl tls root show|export` and `tls local ...`. The download
 * links are the unauthenticated public-certificate routes a device uses;
 * compare the downloaded file's fingerprint with the one shown here, which
 * arrives over your signed-in session. The private key is never shown.
 */
@Component({
  imports: [FormsModule, OperationProgress, Loading, Permit, StateMessage, CopyCode, WhenPipe, Dialog],
  selector: 'app-trust',
  templateUrl: './trust.html',
  styles: [
    'code, pre { overflow-wrap: anywhere; white-space: pre-wrap; }',
    '.fingerprint { font-family: var(--font-mono); }',
  ],
})
export class Trust implements OnInit, OnDestroy {
  protected readonly api = inject(ApiService);
  protected readonly guides = TRUST_GUIDES;

  protected readonly report = signal<LocalCaReport | null>(null);
  protected readonly loading = signal(true);
  protected readonly loadError = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);
  protected readonly plan = signal<LocalCaRotationPlan | null>(null);
  /** The plan approved here, kept as its stamped permit (S309). */
  protected readonly approved = signal<ApprovedPlan | null>(null);
  /** Why the appliance would not approve the plan on screen. */
  protected readonly refused = signal<string | null>(null);
  private readonly sessions = inject(SessionService);
  protected readonly confirmed = signal(false);
  protected readonly operationId = signal<number | null>(null);
  protected readonly operation = signal<OperationState | null>(null);
  private stream?: Subscription;

  protected readonly sort = signal<SortState | null>(null);
  protected readonly sortedLeaves = computed(() =>
    sortRows(this.report()?.leaves ?? [], this.sort(), (leaf, key) =>
      key === 'status' ? leaf.status : key === 'expires' ? (leaf.not_after ?? '') : leaf.host,
    ),
  );

  protected sortBy(key: string): void {
    this.sort.set(nextSort(this.sort(), key));
  }

  protected ariaFor(key: string): 'ascending' | 'descending' | 'none' {
    return ariaSort(this.sort(), key);
  }

  protected dirFor(key: string): string {
    return this.sort()?.key === key ? this.sort()!.dir : '';
  }

  /** Badge for a leaf status: ok is fine, renewal-due is a heads-up, the rest need action. */
  protected leafBadge(status: string): string {
    if (status === 'ok') return 'badge-ok';
    if (status === 'renewal-due') return 'badge-warn';
    return 'badge-fail';
  }

  ngOnInit(): void {
    this.load();
    this.loadAddress();
  }

  // Dashboard address (S289, ADR-035): the names the dashboard answers on
  // from other devices on the network. The .local name is listed first.
  protected readonly address = signal<DashboardAddress | null>(null);
  protected readonly addressName = signal('');
  protected readonly addressExposure = signal<'local' | 'overlay'>('local');
  protected readonly addressError = signal<string | null>(null);

  /** The hosts-file line for a device without router DNS. */
  protected hostsLine(a: DashboardAddress): string {
    return `<appliance IP address>  ${a.name}`;
  }

  loadAddress(): void {
    this.api.dashboardAddress().subscribe({
      next: (a) => {
        this.address.set(a);
        this.addressName.set(a.name);
        this.addressExposure.set(a.exposure);
      },
      error: (failure: HttpErrorResponse) => this.addressError.set(this.describe(failure)),
    });
  }

  saveAddress(): void {
    this.busy.set(true);
    this.addressError.set(null);
    this.api.setDashboardAddress(this.addressName().trim(), this.addressExposure()).subscribe({
      next: (started) => {
        this.busy.set(false);
        this.notice.set(`Setting the dashboard address to ${this.addressName().trim()}.`);
        this.follow(started.id);
        this.loadAddress();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.addressError.set(this.describe(failure));
      },
    });
  }

  ngOnDestroy(): void {
    this.stream?.unsubscribe();
  }

  load(): void {
    this.loading.set(true);
    this.loadError.set(null);
    this.api.localCaStatus().subscribe({
      next: (report) => {
        this.report.set(report);
        this.loading.set(false);
      },
      error: (failure: HttpErrorResponse) => {
        this.loading.set(false);
        this.loadError.set(this.describe(failure));
      },
    });
  }

  protected describe(failure: HttpErrorResponse): string {
    if (failure.status === 0) {
      return "Can't reach the appliance. Check that homestead-server is running.";
    }
    const body = failure.error;
    if (typeof body === 'string' && body.trim()) {
      return body.trim();
    }
    return failure.message;
  }

  create(): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.localCaCreate().subscribe({
      next: (result) => {
        this.busy.set(false);
        this.notice.set(
          result.created ? 'Internal CA created.' : 'The internal CA already exists; nothing changed.',
        );
        this.load();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(this.describe(failure));
      },
    });
  }

  protected readonly confirmingReissueAll = signal(false);

  askReissueAll(): void {
    this.confirmingReissueAll.set(true);
  }

  cancelReissueAll(): void {
    this.confirmingReissueAll.set(false);
  }

  confirmReissueAll(): void {
    this.confirmingReissueAll.set(false);
    this.reissue();
  }

  reissue(host = ''): void {
    this.busy.set(true);
    this.error.set(null);
    this.api.localCaReissue(host).subscribe({
      next: (started) => {
        this.busy.set(false);
        this.notice.set(host ? `Re-issuing the certificate for ${host}.` : 'Re-issuing every local certificate.');
        this.follow(started.id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(this.describe(failure));
      },
    });
  }

  previewRotation(keepRefusal = false): void {
    this.error.set(null);
    this.confirmed.set(false);
    this.approved.set(null);
    if (!keepRefusal) {
      this.refused.set(null);
    }
    this.api.localCaRotationPlan().subscribe({
      next: (plan) => this.plan.set(plan),
      error: (failure: HttpErrorResponse) => this.error.set(this.describe(failure)),
    });
  }

  setConfirmed(value: boolean): void {
    this.confirmed.set(value);
  }

  approveRotation(): void {
    const plan = this.plan();
    if (!plan || !this.confirmed()) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.localCaRotate(plan.digest).subscribe({
      next: (started) => {
        this.busy.set(false);
        this.refused.set(null);
        this.approved.set(
          approvedPlan('Rotate the root', plan.digest, plan.devices_must_retrust, this.sessions.username()),
        );
        this.plan.set(null);
        this.notice.set('Root rotation started. Every device must trust the new root.');
        this.follow(started.id);
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        // A fresh preview replaces the plan; the refusal goes on its permit (S309).
        this.refused.set(this.describe(failure));
        this.previewRotation(true);
      },
    });
  }

  private follow(id: number): void {
    this.stream?.unsubscribe();
    this.operationId.set(id);
    this.operation.set(null);
    this.stream = this.api.operationEvents(id).subscribe({
      next: (state) => {
        this.operation.set(state);
        if (TERMINAL.has(state.status)) {
          this.load();
        }
      },
      error: () => this.load(),
    });
  }
}
