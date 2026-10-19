import { NgComponentOutlet } from '@angular/common';
import { Component, ElementRef, HostListener, computed, effect, inject, signal, untracked, viewChild } from '@angular/core';
import { NavigationEnd, Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { filter } from 'rxjs';
import { SessionService } from './auth/session.service';
import { ApiService, Identity } from './core/api.service';
import { NAV_GROUPS, NavGroup, visibleGroups } from './core/nav';
import { ThemeService } from './core/theme.service';
import { SetupService } from './setup/setup.service';
import { WorkOrders } from './core/work-orders.service';
import { Reauth } from './core/reauth';
import { ToastRegion } from './ui/toast-region';
import { WorkOrdersRail } from './ui/work-orders-rail';

/**
 * localStorage key for explicit sidebar group choices: group id to open
 * (true) or closed (false). A group with no choice is open only while the
 * current page is inside it.
 */
export const NAV_GROUPS_KEY = 'homestead.nav.groups';

/** Width at and above which the sidebar is a column rather than a drawer (md). */
const DESKTOP_QUERY = '(min-width: 48rem)';

function readChoices(): Record<string, boolean> {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(NAV_GROUPS_KEY) ?? '{}');
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      return {};
    }
    return Object.fromEntries(
      Object.entries(parsed).filter((entry): entry is [string, boolean] => typeof entry[1] === 'boolean'),
    );
  } catch {
    return {};
  }
}

function groupOf(url: string): string | null {
  const path = url.split(/[?#]/)[0];
  const current = NAV_GROUPS.find((group) =>
    group.items.some((item) => path === item.path || path.startsWith(`${item.path}/`)),
  );
  return current?.id ?? null;
}

/**
 * Homestead dashboard shell (S282): skip link, grouped sidebar navigation
 * with collapsible groups, a drawer below the md breakpoint, and the routed
 * content. Logged-out visitors see Status, Login and, while the appliance
 * is unclaimed, Claim. The sidebar keeps an empty slot for the S283
 * work-orders rail, which becomes a bottom sheet below md.
 */
@Component({
  imports: [RouterOutlet, RouterLink, RouterLinkActive, WorkOrdersRail, ToastRegion, NgComponentOutlet],
  selector: 'app-root',
  styleUrl: './app.css',
  templateUrl: './app.html',
})
export class App {
  protected readonly sessions = inject(SessionService);
  private readonly api = inject(ApiService);
  private readonly router = inject(Router);
  private readonly workOrders = inject(WorkOrders);
  private readonly setup = inject(SetupService);
  protected readonly reauth = inject(Reauth);
  // Injected so a stored theme choice is applied at startup.
  protected readonly theme = inject(ThemeService);

  private readonly identity = signal<Identity | null>(null);
  protected readonly drawerOpen = signal(false);
  protected readonly sheetShown = computed(() => this.workOrders.orders().length > 0);
  /** At md and wider the rail sits in the sidebar; below it is a bottom sheet. */
  protected readonly desktop = signal(
    typeof matchMedia !== 'function' || matchMedia(DESKTOP_QUERY).matches,
  );
  protected readonly choices = signal<Record<string, boolean>>(readChoices());
  private readonly currentGroup = signal<string | null>(groupOf(this.router.url));
  protected readonly groups = computed<NavGroup[]>(() =>
    visibleGroups(this.sessions.isLoggedIn(), this.identity()?.state === 'UNCLAIMED', this.setup.navLabel()),
  );

  private readonly menuButton = viewChild<ElementRef<HTMLElement>>('menuButton');
  private readonly closeButton = viewChild<ElementRef<HTMLElement>>('closeButton');
  private readonly sidebar = viewChild<ElementRef<HTMLElement>>('sidebar');
  private readonly main = viewChild<ElementRef<HTMLElement>>('main');

  constructor() {
    this.loadIdentity();
    // Operations already running (started before a reload, or from the CLI) join the rail.
    effect(() => {
      if (this.sessions.isLoggedIn()) {
        this.workOrders.restore(true);
        // The Setup entry needs the walkthrough status (S287). A failure
        // leaves the entry out; it never blocks the shell.
        untracked(() => this.setup.setupStatus().subscribe({ error: () => this.setup.clear() }));
      } else {
        untracked(() => this.setup.clear());
      }
    });
    this.router.events.pipe(filter((event) => event instanceof NavigationEnd)).subscribe((event) => {
      this.enter(event.urlAfterRedirects);
      if (this.drawerOpen()) {
        this.close(false);
        this.main()?.nativeElement.focus({ preventScroll: true });
      }
      if (this.sessions.isLoggedIn()) {
        this.workOrders.restore();
      }
      // Claim state only changes while unclaimed or unknown; once claimed, stop asking.
      if (this.identity()?.state !== 'CLAIMED') {
        this.loadIdentity();
      }
    });
    if (typeof matchMedia === 'function') {
      matchMedia(DESKTOP_QUERY).addEventListener('change', (query) => {
        this.desktop.set(query.matches);
        if (query.matches && this.drawerOpen()) {
          this.close(false);
        }
      });
    }
  }

  protected isOpen(group: NavGroup): boolean {
    return this.choices()[group.id] ?? group.id === this.currentGroup();
  }

  protected toggleGroup(group: NavGroup): void {
    this.setChoices({ ...this.choices(), [group.id]: !this.isOpen(group) });
  }

  protected openDrawer(): void {
    this.drawerOpen.set(true);
    setTimeout(() => this.closeButton()?.nativeElement.focus());
  }

  protected close(returnFocus = true): void {
    if (!this.drawerOpen()) {
      return;
    }
    this.drawerOpen.set(false);
    if (returnFocus) {
      this.menuButton()?.nativeElement.focus();
    }
  }

  @HostListener('document:keydown.escape')
  protected onEscape(): void {
    this.close();
  }

  /** Keeps Tab inside the open drawer. */
  protected trapFocus(event: Event): void {
    if (!this.drawerOpen()) {
      return;
    }
    const root = this.sidebar()?.nativeElement;
    const items = Array.from(root?.querySelectorAll<HTMLElement>('a[href], button') ?? []).filter(
      (item) => !item.closest('[hidden]'),
    );
    if (items.length === 0) {
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const backwards = (event as KeyboardEvent).shiftKey;
    if (backwards && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!backwards && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }

  /** The skip link: a bare #main href would navigate the router, so focus directly. */
  protected skipToMain(event: Event): void {
    event.preventDefault();
    this.main()?.nativeElement.focus({ preventScroll: true });
  }

  protected logout(): void {
    this.sessions.logout().subscribe();
  }

  private loadIdentity(): void {
    this.api.identity().subscribe({
      next: (identity) => this.identity.set(identity),
      error: () => this.identity.set(null),
    });
  }

  /** Moving to a page opens its group, even one the owner closed earlier. */
  private enter(url: string): void {
    const id = groupOf(url);
    this.currentGroup.set(id);
    if (id !== null && this.choices()[id] === false) {
      const { [id]: _closed, ...rest } = this.choices();
      this.setChoices(rest);
    }
  }

  private setChoices(choices: Record<string, boolean>): void {
    this.choices.set(choices);
    try {
      localStorage.setItem(NAV_GROUPS_KEY, JSON.stringify(choices));
    } catch {
      // Private browsing: the choice holds for this tab only.
    }
  }
}
