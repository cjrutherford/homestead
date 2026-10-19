import { HttpErrorResponse } from '@angular/common/http';
import { Component, OnInit, computed, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { ApiService } from '../core/api.service';
import { httpErrorMessage } from '../core/http-error';
import { ToastService } from '../core/toast.service';
import { StateMessage } from '../state/state-message';
import { Loading } from '../ui/loading';
import {
  SetupAccess,
  SetupProvider,
  SetupService,
  SetupStatus,
  SetupStep,
  SetupStepChange,
  SetupStepName,
} from './setup.service';

const GLYPH_STATUS: Record<string, string> = {
  done: 'succeeded',
  skipped: 'cancelled',
  pending: 'pending',
};
const WORDS: Record<string, string> = { done: 'Done', skipped: 'Skipped', pending: 'To do' };

/**
 * Post-claim setup walkthrough (S287, ADR-034): account, sign-in, domain and
 * access, first apps. It is orchestration only. It reads what is true,
 * records skips, acknowledgements and the non-sensitive provider and access
 * choices, and sends the owner to the existing pages for every change. The
 * one thing typed here is a DNS credential, which goes straight to the
 * sealed secret route and is cleared from the form as soon as it is sent.
 */
@Component({
  imports: [Loading, RouterLink, StateMessage],
  selector: 'app-setup',
  templateUrl: './setup.html',
})
export class Setup implements OnInit {
  private readonly setup = inject(SetupService);
  private readonly api = inject(ApiService);
  private readonly toast = inject(ToastService);

  protected readonly status = signal<SetupStatus | null>(null);
  protected readonly loading = signal(true);
  protected readonly busy = signal(false);
  protected readonly error = signal<string | null>(null);
  protected readonly failed = signal(false);
  /** The step the owner has open. Defaults to the first one still to do. */
  protected readonly open = signal<SetupStepName | null>(null);

  protected readonly current = computed<SetupStepName | null>(
    () => this.open() ?? (this.status()?.current || null),
  );
  protected readonly provider = computed<SetupProvider | null>(() => {
    const status = this.status();
    return status?.providers.find((p) => p.name === status.choices.provider) ?? null;
  });
  protected readonly access = computed<SetupAccess[]>(() => this.status()?.access ?? []);
  protected readonly missingCredentials = computed(() => {
    const status = this.status();
    return !!this.provider()?.credentials?.length && !status?.facts.domain.credentials_sealed;
  });

  ngOnInit(): void {
    this.refresh();
  }

  protected refresh(): void {
    this.setup.setupStatus().subscribe({
      next: (status) => {
        this.loading.set(false);
        this.failed.set(false);
        this.status.set(status);
      },
      error: (failure: HttpErrorResponse) => {
        this.loading.set(false);
        this.failed.set(true);
        this.error.set(httpErrorMessage(failure, 'setup status'));
      },
    });
  }

  protected word(step: SetupStep): string {
    return WORDS[step.state] ?? step.state;
  }

  protected glyph(step: SetupStep): string {
    return GLYPH_STATUS[step.state] ?? 'pending';
  }

  protected show(name: SetupStepName): void {
    this.open.set(name);
  }

  protected skip(name: SetupStepName): void {
    this.change(name, { state: 'skipped' }, 'Skipped. You can come back to it any time.');
  }

  protected unskip(name: SetupStepName): void {
    this.change(name, { state: 'pending' }, 'Back on your list.');
  }

  protected confirm(name: SetupStepName, message: string): void {
    this.change(name, { state: 'done' }, message);
  }

  protected chooseProvider(name: string): void {
    this.change('domain', { provider: name });
  }

  protected chooseAccess(id: string): void {
    this.change('domain', { access: id });
  }

  /**
   * Seals the DNS credential typed into the form. The values leave the form
   * straight for the secret route and the form is cleared either way.
   */
  protected saveCredentials(form: HTMLFormElement): void {
    const provider = this.provider();
    if (!provider || this.busy()) {
      return;
    }
    const data = new FormData(form);
    const values: Record<string, string> = {};
    for (const field of provider.credentials ?? []) {
      values[field.name] = String(data.get(field.name) ?? '');
    }
    if (Object.values(values).some((value) => value === '')) {
      this.error.set('Fill in every credential field.');
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.api.tlsIssuanceCredentials(provider.name, values).subscribe({
      next: () => {
        form.reset();
        this.busy.set(false);
        this.toast.show(`Stored the ${provider.label} credentials. They cannot be read back.`);
        this.refresh();
      },
      error: (failure: HttpErrorResponse) => {
        form.reset();
        this.busy.set(false);
        this.error.set(httpErrorMessage(failure, 'the credentials'));
      },
    });
  }

  private change(name: SetupStepName, change: SetupStepChange, message?: string): void {
    if (this.busy()) {
      return;
    }
    this.busy.set(true);
    this.error.set(null);
    this.setup.setupStep(name, change).subscribe({
      next: (status) => {
        this.busy.set(false);
        this.status.set(status);
        this.open.set(null);
        if (message) {
          this.toast.show(message);
        }
      },
      error: (failure: HttpErrorResponse) => {
        this.busy.set(false);
        this.error.set(httpErrorMessage(failure, 'setup'));
      },
    });
  }
}
