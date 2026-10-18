import { Component, ElementRef, HostListener, computed, inject, signal, viewChild } from '@angular/core';
import { NavigationEnd, Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { filter } from 'rxjs';
import { SessionService } from './auth/session.service';
import { ApiService, Identity } from './core/api.service';
import { NAV_GROUPS, NavGroup, visibleGroups } from './core/nav';
import { ThemeService } from './core/theme.service';

/** localStorage key for the collapsed sidebar group ids. */
export const NAV_COLLAPSED_KEY = 'homestead.nav.collapsed';

/** Width at and above which the sidebar is a column rather than a drawer (md). */
const DESKTOP_QUERY = '(min-width: 48rem)';

function readCollapsed(): string[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(NAV_COLLAPSED_KEY) ?? '[]');
    return Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : [];
  } catch {
    return [];
  }
}

/**
 * Homestead dashboard shell (S282): skip link, grouped sidebar navigation
 * with collapsible groups, a drawer below the md breakpoint, and the routed
 * content. Logged-out visitors see Status, Login and, while the appliance
 * is unclaimed, Claim. The sidebar keeps an empty slot for the S283
 * work-orders rail.
 */
@Component({
  imports: [RouterOutlet, RouterLink, RouterLinkActive],
  selector: 'app-root',
  styleUrl: './app.css',
  templateUrl: './app.html',
})
export class App {
  protected readonly sessions = inject(SessionService);
  private readonly api = inject(ApiService);
  private readonly router = inject(Router);
  // Injected so a stored theme choice is applied at startup.
  protected readonly theme = inject(ThemeService);

  private readonly identity = signal<Identity | null>(null);
  protected readonly drawerOpen = signal(false);
  protected readonly collapsed = signal<string[]>(readCollapsed());
  protected readonly groups = computed<NavGroup[]>(() =>
    visibleGroups(this.sessions.isLoggedIn(), this.identity()?.state === 'UNCLAIMED'),
  );

  private readonly menuButton = viewChild<ElementRef<HTMLElement>>('menuButton');
  private readonly closeButton = viewChild<ElementRef<HTMLElement>>('closeButton');
  private readonly sidebar = viewChild<ElementRef<HTMLElement>>('sidebar');
  private readonly main = viewChild<ElementRef<HTMLElement>>('main');

  constructor() {
    this.loadIdentity();
    this.router.events.pipe(filter((event) => event instanceof NavigationEnd)).subscribe((event) => {
      this.expandCurrent(event.urlAfterRedirects);
      if (this.drawerOpen()) {
        this.close(false);
        this.main()?.nativeElement.focus();
      }
      // Claim state only changes while unclaimed or unknown; once claimed, stop asking.
      if (this.identity()?.state !== 'CLAIMED') {
        this.loadIdentity();
      }
    });
    if (typeof matchMedia === 'function') {
      matchMedia(DESKTOP_QUERY).addEventListener('change', (query) => {
        if (query.matches && this.drawerOpen()) {
          this.close(false);
        }
      });
    }
  }

  protected isOpen(group: NavGroup): boolean {
    return !this.collapsed().includes(group.id);
  }

  protected toggleGroup(group: NavGroup): void {
    const next = this.isOpen(group)
      ? [...this.collapsed(), group.id]
      : this.collapsed().filter((id) => id !== group.id);
    this.setCollapsed(next);
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
    this.main()?.nativeElement.focus();
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

  /** The group holding the current page never stays collapsed. */
  private expandCurrent(url: string): void {
    const path = url.split(/[?#]/)[0];
    const current = NAV_GROUPS.find((group) =>
      group.items.some((item) => path === item.path || path.startsWith(`${item.path}/`)),
    );
    if (current && this.collapsed().includes(current.id)) {
      this.setCollapsed(this.collapsed().filter((id) => id !== current.id));
    }
  }

  private setCollapsed(ids: string[]): void {
    this.collapsed.set(ids);
    try {
      localStorage.setItem(NAV_COLLAPSED_KEY, JSON.stringify(ids));
    } catch {
      // Private browsing: the choice holds for this tab only.
    }
  }
}
