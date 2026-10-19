import { Component, computed, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import {
  ApiService,
  StorageAssignment,
  StorageMount,
  StorageRegistration,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';
import { ariaSort, nextSort, SortState, sortRows } from '../core/sort';
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

/**
 * Storage surfaces (S268): discovered mounts, registration, and class
 * assignment, matching `homesteadctl storage`. Mounted storage only
 * (ADR-013): nothing here formats or partitions. The daemon exposes no
 * unregister or unassign route, so none is offered. Responses are
 * PascalCase because the Go structs carry no json tags.
 */
@Component({
  imports: [Loading, StateMessage, Dialog],
  selector: 'app-storage',
  templateUrl: './storage.html',
  styles: [
    '.storage-path { font-family: var(--font-mono); font-size: 0.94em; overflow-wrap: anywhere; }',
  ],
})
export class StorageSetup implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly classes = STORAGE_CLASSES;
  protected readonly mounts = signal<StorageMount[]>([]);
  protected readonly sort = signal<SortState | null>(null);
  protected readonly sortedMounts = computed(() =>
    sortRows(this.mounts(), this.sort(), (mount, key) =>
      key === 'free' ? (mount.Accessible ? mount.FreeBytes : -1) : mount.Mountpoint,
    ),
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
    actor: string;
  } | null>(null);
  protected readonly confirmingAssign = signal<{
    class: string;
    mountpoint: string;
    actor: string;
    from: string;
    packages: string[];
  } | null>(null);

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
        this.mounts.set(rows);
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

  register(mountpoint: string, label: string, actor: string): void {
    mountpoint = mountpoint.trim();
    label = label.trim();
    actor = actor.trim();
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
    if (!actor) {
      problems['actor'] = 'Enter the name of the owner making this change.';
    }
    this.fieldErrors.set(problems);
    if (Object.keys(problems).length > 0) {
      return;
    }
    const existing = this.registrationFor(mountpoint);
    const pending = this.confirmingRegister();
    const confirmed =
      pending?.mountpoint === mountpoint &&
      pending.label === label &&
      pending.actor === actor;
    if (existing && !confirmed) {
      // Re-registering relabels an approved mount: confirm first.
      this.confirmingRegister.set({ mountpoint, label, actor });
      this.error.set(null);
      return;
    }
    this.confirmingRegister.set(null);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.storageRegister({ mountpoint, label, actor }).subscribe({
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
      this.register(pending.mountpoint, pending.label, pending.actor);
    }
  }

  cancelRegister(): void {
    this.confirmingRegister.set(null);
  }

  assign(cls: string, mountpoint: string, actor: string, acknowledge = false): void {
    mountpoint = mountpoint.trim();
    actor = actor.trim();
    if (!(STORAGE_CLASSES as readonly string[]).includes(cls)) {
      this.error.set('Choose a storage class.');
      return;
    }
    if (!mountpoint || !actor) {
      this.error.set('Assignment needs a registered mount and a named actor.');
      return;
    }
    this.confirmingAssign.set(null);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    const body = {
      class: cls,
      mountpoint,
      actor,
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
            actor,
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
      this.assign(pending.class, pending.mountpoint, pending.actor, true);
    }
  }

  cancelAssign(): void {
    this.confirmingAssign.set(null);
  }
}
