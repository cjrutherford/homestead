import { HttpErrorResponse } from '@angular/common/http';
import {
  Component,
  ElementRef,
  Injector,
  OnInit,
  afterNextRender,
  computed,
  effect,
  inject,
  signal,
  untracked,
  viewChild,
} from '@angular/core';
import { RouterLink } from '@angular/router';
import { httpErrorMessage } from '../core/http-error';
import { WorkOrders } from '../core/work-orders.service';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import { SetupService, SetupStatus, SetupStep, SetupStepName } from './setup.service';
import { AccountStep } from './steps/account-step';
import { AppsStep } from './steps/apps-step';
import { BackupsStep } from './steps/backups-step';
import { DomainStep } from './steps/domain-step';
import { SignInStep } from './steps/sign-in-step';
import { StorageStep } from './steps/storage-step';

const GLYPH_STATUS: Record<string, string> = {
  done: 'succeeded',
  skipped: 'cancelled',
  pending: 'pending',
};
const WORDS: Record<string, string> = { done: 'Done', skipped: 'Skipped', pending: 'To do' };

/**
 * The post-claim setup wizard (S291, ADR-036): account, storage, sign-in,
 * domain and access, apps, then a summary. One step is on screen at a time.
 * Each step does its work inline by embedding the same components the
 * standalone pages use, so every call goes through the same API routes.
 * Next is enabled only when the step is done or skipped; Back always works;
 * it opens at the first step still to do. This component itself only reads
 * the derived status and records skips: the status is the single source of
 * truth and is read again whenever something the steps wrote may have changed
 * it (see setupRefreshInterceptor).
 */
@Component({
  imports: [
    AccountStep,
    AppsStep,
    BackupsStep,
    DomainStep,
    Loading,
    RouterLink,
    SignInStep,
    StateMessage,
    StorageStep,
  ],
  selector: 'app-setup',
  templateUrl: './setup.html',
})
export class Setup implements OnInit {
  protected readonly setup = inject(SetupService);
  private readonly orders = inject(WorkOrders);
  private readonly injector = inject(Injector);

  private readonly heading = viewChild<ElementRef<HTMLElement>>('stepHeading');

  protected readonly status = this.setup.status;
  protected readonly loading = signal(true);
  protected readonly error = signal<string | null>(null);
  protected readonly failed = signal(false);
  /** The step on screen: 0 to 4, or the number of steps for the summary. Null until the status is read. */
  protected readonly index = signal<number | null>(null);

  protected readonly steps = computed<SetupStep[]>(() => this.status()?.steps ?? []);
  protected readonly onSummary = computed(() => {
    const index = this.index();
    return index !== null && index >= this.steps().length;
  });
  protected readonly step = computed<SetupStep | null>(() => {
    const index = this.index();
    return index === null ? null : (this.steps()[index] ?? null);
  });
  protected readonly canNext = computed(() => {
    const step = this.step();
    return !!step && step.state !== 'pending';
  });
  protected readonly pendingSteps = computed(() => this.steps().filter((s) => s.state === 'pending'));

  private lastKey: string | null = null;
  /** Operations already open when the wizard opened: not started from it. */
  private readonly openAtStart = new Set(untracked(() => this.orders.orders().map((o) => o.id)));

  constructor() {
    // Something a step wrote, or an operation it started, may have changed
    // what the status derives from: read it again.
    effect(() => {
      // Writes are covered by the interceptor; an install finishes later, so
      // also count operations that settled and were not already open when the
      // wizard opened.
      const settled = this.orders
        .orders()
        .filter((o) => o.settled && !this.openAtStart.has(o.id)).length;
      const key = `${this.setup.changed()}:${settled}`;
      if (this.lastKey === null) {
        this.lastKey = key;
        return;
      }
      if (key !== this.lastKey) {
        this.lastKey = key;
        untracked(() => this.reload());
      }
    });
    // Moving between steps moves focus to the new step's heading.
    let previous: number | null = null;
    effect(() => {
      const index = this.index();
      const moved = previous !== null && index !== null && index !== previous;
      previous = index;
      if (moved) {
        untracked(() =>
          afterNextRender(() => this.heading()?.nativeElement.focus(), { injector: this.injector }),
        );
      }
    });
  }

  ngOnInit(): void {
    this.refresh();
  }

  protected refresh(): void {
    this.setup.setupStatus().subscribe({
      next: (status) => {
        this.loading.set(false);
        this.failed.set(false);
        if (this.index() === null) {
          this.index.set(this.openingIndex(status));
        }
      },
      error: (failure: HttpErrorResponse) => {
        this.loading.set(false);
        this.failed.set(true);
        this.error.set(httpErrorMessage(failure, 'setup status'));
      },
    });
  }

  /** A quiet read: a failure leaves the last status on screen. */
  private reload(): void {
    this.setup.setupStatus().subscribe({ error: () => undefined });
  }

  /** The first step still to do, or the summary when none is. */
  private openingIndex(status: SetupStatus): number {
    const first = status.steps.findIndex((s) => s.state === 'pending');
    return first === -1 ? status.steps.length : first;
  }

  protected word(step: SetupStep): string {
    if (step.waived && step.state === 'skipped') {
      return 'Not required';
    }
    return WORDS[step.state] ?? step.state;
  }

  protected glyph(step: SetupStep): string {
    return GLYPH_STATUS[step.state] ?? 'pending';
  }

  protected next(): void {
    const index = this.index();
    if (index !== null && this.canNext()) {
      this.index.set(Math.min(index + 1, this.steps().length));
    }
  }

  protected back(): void {
    const index = this.index();
    if (index !== null && index > 0) {
      this.index.set(index - 1);
    }
  }

  protected goTo(name: SetupStepName): void {
    const index = this.steps().findIndex((s) => s.name === name);
    if (index !== -1) {
      this.index.set(index);
    }
  }

  protected skip(name: SetupStepName): void {
    this.setup.change(name, { state: 'skipped' }, 'Skipped. You can come back to it any time.');
  }

  protected unskip(name: SetupStepName): void {
    this.setup.change(name, { state: 'pending' }, 'Back on your list.');
  }
}
