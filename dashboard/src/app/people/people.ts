import { HttpErrorResponse } from '@angular/common/http';
import { Component, computed, inject, OnInit, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { Observable } from 'rxjs';
import { ToastService } from '../core/toast.service';
import { StateMessage } from '../state/state-message';
import { Dialog } from '../ui/dialog';
import { Loading } from '../ui/loading';
import { DriftAction, PeopleService, PeopleView, Person, PersonGrant, Revealed } from './people.service';

type Access = 'none' | 'user' | 'admin';
type HowTo = 'link' | 'generated' | 'password';

/** The one dialog open at a time; the page keeps its state. */
export type PeopleDialog =
  | { kind: 'add' }
  | { kind: 'edit'; person: Person }
  | { kind: 'apps'; person: Person }
  | { kind: 'reset'; person: Person }
  | { kind: 'confirm'; person: Person; action: 'mfa' | 'disable' | 'enable' | 'remove' | 'forget' }
  | { kind: 'reveal'; who: string; revealed: Revealed }
  | { kind: 'added'; person: Person; reason: string };

/**
 * People (S317, ADR-043): who can open which apps. People are never
 * Homestead administrators; this page only decides app access. Every
 * change is confirmed here, and a generated password or link is shown
 * once and not kept.
 */
@Component({
  selector: 'app-people',
  imports: [RouterLink, StateMessage, Loading, Dialog],
  templateUrl: './people.html',
})
export class People implements OnInit {
  private readonly service = inject(PeopleService);
  private readonly toast = inject(ToastService);

  protected readonly view = signal<PeopleView | null>(null);
  protected readonly loading = signal(true);
  protected readonly error = signal<string | null>(null);
  protected readonly problem = signal<string | null>(null);
  protected readonly busy = signal(false);
  protected readonly dialog = signal<PeopleDialog | null>(null);

  protected readonly apps = computed(() => this.view()?.apps ?? []);
  protected adminOnly(app: string): boolean {
    return (this.view()?.admin_only ?? []).includes(app);
  }
  protected readonly people = computed(() => this.view()?.people ?? []);
  protected readonly unmanaged = computed(() => this.view()?.unmanaged ?? []);
  protected readonly canAct = computed(() => this.view()?.token_kept === true);
  protected readonly lead = computed(() => {
    const v = this.view();
    if (this.error()) {
      return 'People cannot be read.';
    }
    if (!v) {
      return 'Reading who can open what.';
    }
    if (!v.token_kept) {
      return 'Homestead cannot manage people yet.';
    }
    const n = this.people().length;
    return n === 0 ? 'Only you can open the apps.' : n === 1 ? '1 person besides you can open apps.' : `${n} people besides you can open apps.`;
  });

  // Form state for the dialogs.
  protected formName = '';
  protected formUsername = '';
  protected formEmail = '';
  protected formHow: HowTo = 'link';
  protected formPassword = '';
  protected formAccess: Record<string, Access> = {};

  ngOnInit(): void {
    this.load();
  }

  protected load(): void {
    this.service.people().subscribe({
      next: (view) => {
        this.view.set(view);
        this.error.set(null);
        this.loading.set(false);
      },
      error: (err: HttpErrorResponse) => {
        this.error.set(
          err.status === 401 || err.status === 403
            ? 'Sign in as the owner to manage people.'
            : "Can't read the people. Check that homestead-server is running.",
        );
        this.loading.set(false);
      },
    });
  }

  // ---- reading helpers

  protected roleOf(person: Person, app: string): Access {
    return (person.grants ?? []).find((g) => g.app === app)?.role ?? 'none';
  }

  protected grantsOf(person: Person): PersonGrant[] {
    return person.grants ?? [];
  }

  protected driftOf(person: Person) {
    return person.drift ?? [];
  }

  protected driftLabel(d: { kind: string; field?: string; app?: string }): string {
    return d.kind === 'grant' ? `Access to ${d.app}` : (d.field ?? 'A detail').replace(/^./, (c) => c.toUpperCase());
  }

  // ---- opening dialogs

  protected close(): void {
    this.dialog.set(null);
    this.problem.set(null);
    this.formPassword = '';
  }

  protected openAdd(): void {
    this.formName = '';
    this.formUsername = '';
    this.formEmail = '';
    this.formHow = 'link';
    this.formPassword = '';
    this.formAccess = {};
    this.problem.set(null);
    this.dialog.set({ kind: 'add' });
  }

  protected openEdit(person: Person): void {
    this.formName = person.display_name;
    this.formUsername = person.username;
    this.formEmail = person.email;
    this.problem.set(null);
    this.dialog.set({ kind: 'edit', person });
  }

  protected openApps(person: Person): void {
    this.formAccess = Object.fromEntries(this.apps().map((app) => [app, this.roleOf(person, app)]));
    this.problem.set(null);
    this.dialog.set({ kind: 'apps', person });
  }

  protected openReset(person: Person): void {
    this.formHow = 'link';
    this.formPassword = '';
    this.problem.set(null);
    this.dialog.set({ kind: 'reset', person });
  }

  protected openConfirm(person: Person, action: 'mfa' | 'disable' | 'enable' | 'remove' | 'forget'): void {
    this.problem.set(null);
    this.dialog.set({ kind: 'confirm', person, action });
  }

  protected adminChosen(): boolean {
    return Object.values(this.formAccess).includes('admin');
  }

  // ---- doing

  /** Runs one change; on failure the reason shows in the open dialog or the page. */
  private run<T>(call: Observable<T>, done: (result: T) => void): void {
    if (this.busy()) {
      return;
    }
    this.busy.set(true);
    this.problem.set(null);
    call.subscribe({
      next: (result) => {
        this.busy.set(false);
        done(result);
      },
      error: (err: HttpErrorResponse) => {
        this.busy.set(false);
        this.problem.set(this.explain(err));
        if (err.headers?.get('X-Homestead-Error') === 'people-no-token') {
          this.dialog.set(null);
          this.load();
        }
      },
    });
  }

  private explain(err: HttpErrorResponse): string {
    const body = typeof err.error === 'string' ? err.error.trim() : '';
    if (err.status === 0) {
      return 'Homestead could not be reached. Nothing was changed.';
    }
    return body || `That did not work (${err.status}).`;
  }

  private warn(result: { warnings?: string[] } | null | undefined): void {
    for (const w of result?.warnings ?? []) {
      this.toast.show(`Warning: ${w}`, 'notice');
    }
  }

  private finished(message: string): void {
    this.dialog.set(null);
    this.toast.show(message);
    this.load();
  }

  private reveal(who: string, revealed: Revealed | null | undefined, message: string): void {
    if (revealed) {
      this.dialog.set({ kind: 'reveal', who, revealed });
      this.load();
    } else {
      this.finished(message);
    }
  }

  protected submitAdd(): void {
    const grants: PersonGrant[] = Object.entries(this.formAccess)
      .filter(([, role]) => role !== 'none')
      .map(([app, role]) => ({ app, role: role as 'user' | 'admin' }));
    const body = {
      display_name: this.formName.trim(),
      username: this.formUsername.trim(),
      email: this.formEmail.trim(),
      grants,
      ...(this.formHow === 'password' ? { password: this.formPassword } : { method: this.formHow }),
    };
    this.run(this.service.addPerson(body), (result) => {
      this.formPassword = '';
      if (result.sign_in_failed && !result.revealed) {
        // ADR-049 §4: the person is kept; the dialog stays open on them.
        this.dialog.set({ kind: 'added', person: result.person, reason: result.warning ?? '' });
        this.load();
        return;
      }
      if (result.warning) {
        this.toast.show(result.warning, 'notice');
      }
      this.reveal(result.person.display_name, result.revealed, `${result.person.display_name} added`);
    });
  }

  protected submitEdit(): void {
    const d = this.dialog();
    if (d?.kind !== 'edit') {
      return;
    }
    this.run(
      this.service.editPerson(d.person.id, { display_name: this.formName.trim(), username: this.formUsername.trim(), email: this.formEmail.trim() }),
      () => this.finished(`${this.formName.trim()} updated`),
    );
  }

  protected submitApps(): void {
    const d = this.dialog();
    if (d?.kind !== 'apps') {
      return;
    }
    const person = d.person;
    const changes = this.apps().filter((app) => this.formAccess[app] !== this.roleOf(person, app));
    if (changes.length === 0) {
      this.close();
      return;
    }
    const step = (i: number): void => {
      if (i >= changes.length) {
        this.finished(`${person.display_name}'s apps updated`);
        return;
      }
      const app = changes[i];
      const want = this.formAccess[app];
      const call = want === 'none' ? this.service.revokeApp(person.id, app) : this.service.grantApp(person.id, app, want === 'admin');
      this.run(call, (r) => {
        this.warn(r);
        step(i + 1);
      });
    };
    step(0);
  }

  protected submitReset(): void {
    const d = this.dialog();
    if (d?.kind !== 'reset') {
      return;
    }
    const given = this.formHow === 'password';
    this.run(this.service.resetPassword(d.person.id, given ? 'link' : (this.formHow as 'link' | 'generated'), given ? this.formPassword : ''), (result) => {
      this.formPassword = '';
      this.reveal(d.person.display_name, result.revealed, `Password for ${d.person.display_name} set`);
    });
  }

  /** After a failed first sign-in, a generated password, shown once. */
  protected generateInstead(): void {
    const d = this.dialog();
    if (d?.kind !== 'added') {
      return;
    }
    this.run(this.service.resetPassword(d.person.id, 'generated'), (result) => {
      this.reveal(d.person.display_name, result.revealed, `Password for ${d.person.display_name} set`);
    });
  }

  protected submitConfirm(): void {
    const d = this.dialog();
    if (d?.kind !== 'confirm') {
      return;
    }
    const { person, action } = d;
    const name = person.display_name;
    switch (action) {
      case 'mfa':
        this.run(this.service.resetSecondFactor(person.id), (r) =>
          this.finished(`Second factor reset for ${name}: ${r.removed ?? 0} removed${r.skipped ? `, ${r.skipped} left` : ''}`),
        );
        break;
      case 'disable':
        this.run(this.service.disablePerson(person.id), (r) => {
          this.warn(r);
          this.finished(`${name} disabled`);
        });
        break;
      case 'enable':
        this.run(this.service.enablePerson(person.id), (r) => {
          this.warn(r);
          this.finished(`${name} enabled`);
        });
        break;
      case 'remove':
        this.run(this.service.removePerson(person.id), (r) => {
          this.warn(r);
          this.finished(`${name} removed. App data stays in the apps.`);
        });
        break;
      case 'forget':
        this.resolve(person, 'forget');
        break;
    }
  }

  protected resolve(person: Person, action: DriftAction, method: 'link' | 'generated' = 'link'): void {
    this.run(this.service.resolveDrift(person.id, action, method), (r) => {
      const words: Record<DriftAction, string> = {
        'put-back': `Authentik put back as Homestead has it for ${person.display_name}`,
        keep: `Authentik's values kept for ${person.display_name}`,
        recreate: `${person.display_name} recreated in Authentik`,
        forget: `${person.display_name} removed from Homestead`,
      };
      this.reveal(person.display_name, r.revealed, words[action]);
    });
  }

  protected adopt(username: string): void {
    this.run(this.service.adoptPerson(username), (r) => this.finished(`${r.person.display_name} is now managed by Homestead`));
  }

  protected copy(value: string): void {
    const clipboard = typeof navigator === 'undefined' ? undefined : navigator.clipboard;
    if (!clipboard) {
      this.toast.show('Copy is not available here. Select it and copy.', 'notice');
      return;
    }
    clipboard.writeText(value).then(
      () => this.toast.show('Copied'),
      () => this.toast.show('Copy was refused. Select it and copy.', 'notice'),
    );
  }

  protected confirmWords(action: 'mfa' | 'disable' | 'enable' | 'remove' | 'forget'): { heading: string; body: string; button: string; danger: boolean } {
    switch (action) {
      case 'mfa':
        return { heading: 'Reset second factor?', body: 'Their authenticator and security keys are removed from Authentik. They set up a new one the next time they sign in.', button: 'Reset second factor', danger: true };
      case 'disable':
        return { heading: 'Disable this person?', body: 'They cannot sign in to Authentik or the apps. Their access is kept so you can enable them again. Sessions they already have may last until they expire.', button: 'Disable', danger: false };
      case 'enable':
        return { heading: 'Enable this person?', body: 'They can sign in again with the access they had.', button: 'Enable', danger: false };
      case 'remove':
        return { heading: 'Remove this person?', body: 'Their Authentik account is deleted and they lose every app. App data stays in the apps; remove it there if you want it gone.', button: 'Remove', danger: true };
      case 'forget':
        return { heading: 'Remove from Homestead?', body: 'This person is already gone from Authentik. Homestead forgets them. Nothing else changes.', button: 'Remove from Homestead', danger: true };
    }
  }
}
