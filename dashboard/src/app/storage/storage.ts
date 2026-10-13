import { Component, inject, OnInit, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import {
  ApiService,
  StorageAssignment,
  StorageMount,
  StorageRegistration,
} from '../core/api.service';
import { StateMessage } from '../state/state-message';

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
  imports: [StateMessage],
  selector: 'app-storage',
  templateUrl: './storage.html',
  styles: [
    '.storage-path { overflow-wrap: anywhere; }',
    'button, input, select { min-height: 44px; }',
  ],
})
export class StorageSetup implements OnInit {
  private readonly api = inject(ApiService);

  protected readonly classes = STORAGE_CLASSES;
  protected readonly mounts = signal<StorageMount[]>([]);
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
  } | null>(null);

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
        this.mountsError.set(failure.error ?? failure.message);
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
        this.registrationsError.set(failure.error ?? failure.message);
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
        this.assignmentsError.set(failure.error ?? failure.message);
      },
    });
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

  /** Why registration would be refused, mirroring the server checks. */
  unfit(mount: StorageMount): string | null {
    if (!mount.Accessible) {
      return 'serves no usage data';
    }
    if (mount.ReadOnly) {
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

  register(mountpoint: string, label: string, actor: string): void {
    mountpoint = mountpoint.trim();
    label = label.trim();
    actor = actor.trim();
    if (!mountpoint || !label || !actor) {
      this.error.set('Registration needs a mountpoint, a label and a named actor.');
      return;
    }
    if (!mountpoint.startsWith('/') || mountpoint.length > 1024) {
      this.error.set('Mountpoint must be an absolute path.');
      return;
    }
    if (!/^[a-z][a-z0-9-]*$/.test(label) || label.length > 32) {
      this.error.set(
        'Label must start with a lowercase letter and use only lowercase letters, digits and hyphens (max 32).',
      );
      return;
    }
    const existing = this.registrationFor(mountpoint);
    if (existing && !this.confirmingRegister()) {
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
        this.error.set(failure.error ?? failure.message);
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

  assign(cls: string, mountpoint: string, actor: string): void {
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
    const current = this.assignmentFor(cls);
    if (current && current.Mountpoint !== mountpoint && !this.confirmingAssign()) {
      this.confirmingAssign.set({ class: cls, mountpoint, actor });
      this.error.set(null);
      return;
    }
    this.confirmingAssign.set(null);
    this.busy.set(true);
    this.error.set(null);
    this.notice.set(null);
    this.api.storageAssign({ class: cls, mountpoint, actor }).subscribe({
      next: (saved) => {
        this.busy.set(false);
        this.notice.set(`Assigned ${saved.Class} to ${saved.Mountpoint}.`);
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(failure.error ?? failure.message);
      },
    });
  }

  confirmAssign(): void {
    const pending = this.confirmingAssign();
    if (pending) {
      this.assign(pending.class, pending.mountpoint, pending.actor);
    }
  }

  cancelAssign(): void {
    this.confirmingAssign.set(null);
  }
}
