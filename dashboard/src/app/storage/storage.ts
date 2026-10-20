import { WhenPipe } from '../core/format-time';
import { Component, computed, inject, OnInit, signal, input } from '@angular/core';
import { NgTemplateOutlet } from '@angular/common';
import { HttpErrorResponse } from '@angular/common/http';
import {
  ApiService,
  StorageAssignment,
  StorageMount,
  StorageRegistration,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { ariaSort, nextSort, SortState, sortRows } from '../core/sort';
import { concatMap, from, Observable } from 'rxjs';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';

/** The five class names the daemon accepts (internal/storage/classes.go). */
export const STORAGE_CLASSES = [
  'application',
  'database',
  'bulk',
  'backup',
  'cache',
] as const;

/** What each class is for, in the owner's words (S293). */
export const STORAGE_CLASS_HELP: Record<(typeof STORAGE_CLASSES)[number], string> = {
  application: 'app settings and small files; every install needs it',
  database: 'app databases; must be a local disk, ideally a fast one',
  bulk: 'large media and libraries, such as photos and films',
  backup: 'backup copies; best on a different disk from the data it protects',
  cache: 'throwaway data apps can rebuild; optional',
};

/**
 * A registration label from a disk label or path: lowercase letters,
 * digits and hyphens, starting with a letter, at most 32 long.
 */
export function storageLabel(mount: StorageMount, taken: Set<string>): string {
  const base =
    (mount.Label || mount.Mountpoint.split('/').filter(Boolean).pop() || '')
      .toLowerCase()
      .replace(/[^a-z0-9-]+/g, '-')
      .replace(/^[^a-z]+/, '')
      .slice(0, 32)
      .replace(/-+$/, '') || 'disk';
  let label = base;
  for (let n = 2; taken.has(label); n++) {
    const suffix = `-${n}`;
    label = base.slice(0, 32 - suffix.length).replace(/-+$/, '') + suffix;
  }
  return label;
}

/** One step of the suggested layout, applied in order. */
interface LayoutStep {
  register?: { mountpoint: string; label: string };
  assign?: { class: string; mountpoint: string };
}

/**
 * Storage surfaces (S268): discovered mounts, registration, and class
 * assignment, matching `homesteadctl storage`. Mounted storage only
 * (ADR-013): nothing here formats or partitions. Unassign and unregister
 * (S288) drop Homestead's record only, behind a danger-confirm dialog; the
 * daemon refuses with a 409 naming apps that still use the mount.
 * S293 lists data disks first, folds system paths away, explains the
 * classes, offers the daemon's suggested layout in one approval, and lets
 * the daemon record the signed-in owner as the actor.
 * Responses are PascalCase because the Go structs carry no json tags.
 */
@Component({
  imports: [Loading, StateMessage, Dialog, WhenPipe, NgTemplateOutlet],
  selector: 'app-storage',
  templateUrl: './storage.html',
  styles: [
    '.storage-path { font-family: var(--font-mono); font-size: 0.94em; overflow-wrap: break-word; }',
  ],
})
export class StorageSetup implements OnInit {
  /** Shown inside the setup wizard (S291): the page's own heading is left to the step. */
  readonly embedded = input(false);
  private readonly api = inject(ApiService);

  protected readonly classes = STORAGE_CLASSES;
  protected readonly classHelp = STORAGE_CLASS_HELP;
  protected readonly mounts = signal<StorageMount[]>([]);
  protected readonly sort = signal<SortState | null>(null);
  protected readonly sortedMounts = computed(() =>
    sortRows(this.mounts(), this.sort(), (mount, key) =>
      key === 'free' ? (mount.Accessible ? mount.FreeBytes : -1) : mount.Mountpoint,
    ),
  );
  /** Data disks (and every mount from a daemon older than S293). */
  protected readonly dataMounts = computed(() =>
    this.sortedMounts().filter((mount) => !mount.kind || mount.kind === 'data'),
  );
  /** System paths and mounts that can never be registered, folded away. */
  protected readonly otherMounts = computed(() =>
    this.sortedMounts().filter((mount) => mount.kind && mount.kind !== 'data'),
  );
  /** The daemon's proposal: classes per mount, for unassigned classes only. */
  protected readonly suggestion = computed(() =>
    this.mounts()
      .filter((mount) => mount.suggested?.length)
      .map((mount) => ({
        mount,
        label: this.registrationFor(mount.Mountpoint)?.Label ?? null,
        classes: mount.suggested!,
      })),
  );
  protected readonly unassigned = computed(() =>
    STORAGE_CLASSES.filter((cls) => !this.assignmentFor(cls)),
  );
  protected readonly registrations = signal<StorageRegistration[]>([]);
  protected readonly assignments = signal<StorageAssignment[]>([]);
  protected readonly mountsError = signal<string | null>(null);
  protected readonly registrationsError = signal<string | null>(null);
  protected readonly assignmentsError = signal<string | null>(null);
  protected readonly mountsLoading = signal(true);
  protected readonly registrationsLoading = signal(true);
  protected readonly assignmentsLoading = signal(true);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly notice = signal<string | null>(null);
  /** Mountpoint chosen from the discovered list to prefill the form. */
  protected readonly picked = signal('');
  protected readonly confirmingRegister = signal<{
    mountpoint: string;
    label: string;
  } | null>(null);
  protected readonly confirmingAssign = signal<{
    class: string;
    mountpoint: string;
    from: string;
    packages: string[];
  } | null>(null);

  protected readonly confirmingUnassign = signal<StorageAssignment | null>(null);
  protected readonly confirmingUnregister = signal<StorageRegistration | null>(null);

  protected sortBy(key: string): void {
    this.sort.set(nextSort(this.sort(), key));
  }

  protected ariaFor(key: string): 'ascending' | 'descending' | 'none' {
    return ariaSort(this.sort(), key);
  }

  protected dirFor(key: string): string {
    return this.sort()?.key === key ? this.sort()!.dir : '';
  }

  ngOnInit(): void {
    this.refresh();
  }

  refresh(): void {
    this.mountsLoading.set(true);
    this.mountsError.set(null);
    this.api.storageMounts().subscribe({
      next: (rows) => {
        this.mountsLoading.set(false);
        // Discovery can return a mount twice (bind mounts, repeated
        // mountinfo lines); list each path once.
        const seen = new Set<string>();
        this.mounts.set(
          rows.filter((mount) => {
            if (seen.has(mount.Mountpoint)) {
              return false;
            }
            seen.add(mount.Mountpoint);
            return true;
          }),
        );
      },
      error: (failure: HttpErrorResponse) => {
        this.mountsLoading.set(false);
        this.mountsError.set(this.describe(failure));
      },
    });
    this.registrationsLoading.set(true);
    this.registrationsError.set(null);
    this.api.storageRegistrations().subscribe({
      next: (rows) => {
        this.registrationsLoading.set(false);
        this.registrations.set(rows);
      },
      error: (failure: HttpErrorResponse) => {
        this.registrationsLoading.set(false);
        this.registrationsError.set(this.describe(failure));
      },
    });
    this.assignmentsLoading.set(true);
    this.assignmentsError.set(null);
    this.api.storageAssignments().subscribe({
      next: (rows) => {
        this.assignmentsLoading.set(false);
        this.assignments.set(rows);
      },
      error: (failure: HttpErrorResponse) => {
        this.assignmentsLoading.set(false);
        this.assignmentsError.set(this.describe(failure));
      },
    });
  }

  /**
   * Readable text for a failed call: status 0 is a network failure (the
   * browser hands back a ProgressEvent), and JSON bodies carry `error`.
   */
  protected describe(failure: HttpErrorResponse): string {
    if (failure.status === 0) {
      return "Can't reach the appliance. Check that homestead-server is running.";
    }
    const body = failure.error;
    if (typeof body === 'string' && body.trim()) {
      return body;
    }
    if (body && typeof body.error === 'string') {
      return body.error;
    }
    return failure.message;
  }

  /** Bytes in binary units, one decimal. */
  size(bytes: number): string {
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024;
      unit++;
    }
    return `${unit === 0 ? value : value.toFixed(1)} ${units[unit]}`;
  }

  /** Why registration would be refused: the daemon's `registrable` verdict, with a fallback for older daemons. */
  unfit(mount: StorageMount): string | null {
    if (mount.registrable === false) {
      return (mount.registrable_reason || 'not registrable').replace(/^storage: /, '');
    }
    if (mount.registrable === undefined && !mount.Accessible) {
      return 'serves no usage data';
    }
    if (mount.registrable === undefined && mount.ReadOnly) {
      return 'read-only';
    }
    return null;
  }

  registrationFor(mountpoint: string): StorageRegistration | undefined {
    return this.registrations().find((r) => r.Mountpoint === mountpoint);
  }

  assignmentFor(cls: string): StorageAssignment | undefined {
    return this.assignments().find((a) => a.Class === cls);
  }

  classesOn(mountpoint: string): string[] {
    return this.assignments()
      .filter((a) => a.Mountpoint === mountpoint)
      .map((a) => a.Class);
  }

  pick(mountpoint: string): void {
    this.picked.set(mountpoint);
  }

  protected readonly fieldErrors = signal<Record<string, string>>({});

  register(mountpoint: string, label: string): void {
    mountpoint = mountpoint.trim();
    label = label.trim();
    const problems: Record<string, string> = {};
    if (!mountpoint) {
      problems['mountpoint'] = 'Enter the mount path, such as /mnt/media.';
    } else if (!mountpoint.startsWith('/') || mountpoint.length > 1024) {
      problems['mountpoint'] = 'The mount path must be an absolute path starting with /.';
    }
    if (!label) {
      problems['label'] = 'Enter a label for the mount.';
    } else if (!/^[a-z][a-z0-9-]*$/.test(label) || label.length > 32) {
      problems['label'] =
        'Start the label with a lowercase letter and use only lowercase letters, digits and hyphens (max 32).';
    }
    this.fieldErrors.set(problems);
    if (Object.keys(problems).length > 0) {
      return;
    }
    const existing = this.registrationFor(mountpoint);
    const pending = this.confirmingRegister();
    const confirmed =
      pending?.mountpoint === mountpoint &&
      pending.label === label;
    if (existing && !confirmed) {
      // Re-registering relabels an approved mount: confirm first.
      this.confirmingRegister.set({ mountpoint, label });
      this.error.set(null);
      return;
    }
    this.confirmingRegister.set(null);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.storageRegister({ mountpoint, label }).subscribe({
      next: (saved) => {
        this.busy.set(false);
        this.notice.set(`Registered ${saved.Mountpoint} as ${saved.Label}.`);
        this.picked.set('');
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(this.describe(failure));
      },
    });
  }

  confirmRegister(): void {
    const pending = this.confirmingRegister();
    if (pending) {
      this.register(pending.mountpoint, pending.label);
    }
  }

  cancelRegister(): void {
    this.confirmingRegister.set(null);
  }

  assign(cls: string, mountpoint: string, acknowledge = false): void {
    mountpoint = mountpoint.trim();
    if (!(STORAGE_CLASSES as readonly string[]).includes(cls)) {
      this.error.set('Choose a storage class.');
      return;
    }
    if (!mountpoint) {
      this.error.set('Choose a registered mount for the class.');
      return;
    }
    this.confirmingAssign.set(null);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    const body = {
      class: cls,
      mountpoint,
      ...(acknowledge ? { acknowledge_impact: true } : {}),
    };
    this.api.storageAssign(body).subscribe({
      next: (saved) => {
        this.busy.set(false);
        this.notice.set(`Assigned ${saved.Class} to ${saved.Mountpoint}.`);
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        const impact = failure.status === 409 ? failure.error : null;
        if (impact && Array.isArray(impact.affected_packages)) {
          // The daemon refuses until the owner acknowledges: ask, and
          // send the acknowledgement only after they confirm.
          this.confirmingAssign.set({
            class: cls,
            mountpoint,
            from: impact.from ?? '',
            packages: impact.affected_packages,
          });
          return;
        }
        this.error.set(this.describe(failure));
      },
    });
  }

  confirmAssign(): void {
    const pending = this.confirmingAssign();
    if (pending) {
      this.assign(pending.class, pending.mountpoint, true);
    }
  }

  cancelAssign(): void {
    this.confirmingAssign.set(null);
  }

  /**
   * Applies the suggested layout (S293): registers each suggested mount
   * that is not registered yet, then assigns each suggested class, one
   * call at a time so a refusal stops the rest. Pressing the button is
   * the owner's approval; every call is audited as usual.
   */
  useSuggestion(): void {
    const taken = new Set(this.registrations().map((r) => r.Label));
    const steps: LayoutStep[] = [];
    for (const item of this.suggestion()) {
      if (!item.label) {
        const label = storageLabel(item.mount, taken);
        taken.add(label);
        steps.push({ register: { mountpoint: item.mount.Mountpoint, label } });
      }
    }
    for (const item of this.suggestion()) {
      for (const cls of item.classes) {
        steps.push({ assign: { class: cls, mountpoint: item.mount.Mountpoint } });
      }
    }
    if (steps.length === 0) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    from(steps)
      .pipe(
        concatMap((step): Observable<unknown> =>
          step.register
            ? this.api.storageRegister(step.register)
            : this.api.storageAssign(step.assign!),
        ),
      )
      .subscribe({
        complete: () => {
          this.busy.set(false);
          this.notice.set('Applied the suggested layout.');
          this.refresh();
        },
        error: (failure: HttpErrorResponse) => {
          this.busy.set(false);
          this.error.set(`The suggested layout stopped part way: ${this.describe(failure)}`);
          this.refresh();
        },
      });
  }

  askUnassign(item: StorageAssignment): void {
    this.error.set(null);
    this.confirmingUnassign.set(item);
  }

  cancelUnassign(): void {
    this.confirmingUnassign.set(null);
  }

  confirmUnassign(): void {
    const pending = this.confirmingUnassign();
    if (!pending) {
      return;
    }
    this.confirmingUnassign.set(null);
    this.drop(this.api.storageUnassign(pending.Class), `Unassigned ${pending.Class} from ${pending.Mountpoint}.`);
  }

  askUnregister(reg: StorageRegistration): void {
    this.error.set(null);
    this.confirmingUnregister.set(reg);
  }

  cancelUnregister(): void {
    this.confirmingUnregister.set(null);
  }

  confirmUnregister(): void {
    const pending = this.confirmingUnregister();
    if (!pending) {
      return;
    }
    this.confirmingUnregister.set(null);
    this.drop(this.api.storageUnregister(pending.Mountpoint), `Unregistered ${pending.Mountpoint}.`);
  }

  private drop(call: Observable<void>, done: string): void {
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    call.subscribe({
      next: () => {
        this.busy.set(false);
        this.notice.set(done);
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(this.describe(failure));
      },
    });
  }
}
