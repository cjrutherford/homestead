import { Component, OnInit, computed, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';
import { forkJoin } from 'rxjs';
import { SessionService } from '../auth/session.service';
import { ApiService, MeView } from '../core/api.service';
import { MyApp, PeopleService } from '../people/people.service';
import { AppIcon } from '../ui/app-icon';
import { Loading } from '../ui/loading';

/**
 * The apps page (ADR-050 §4): the dashboard's root, for the owner and for
 * every person who signs in. A greeting and one tile per app: icon, name,
 * and a link that opens it in this tab. Only /me and /me/apps are called, so
 * a person session stays inside what the server lets it reach.
 */
@Component({
  selector: 'app-home',
  imports: [RouterLink, Loading, AppIcon],
  templateUrl: './home.html',
  styleUrl: './home.css',
})
export class Home implements OnInit {
  private readonly api = inject(ApiService);
  private readonly people = inject(PeopleService);
  protected readonly sessions = inject(SessionService);
  private readonly router = inject(Router);

  protected readonly loading = signal(true);
  protected readonly failed = signal(false);
  protected readonly me = signal<MeView | null>(null);
  protected readonly apps = signal<MyApp[]>([]);

  protected readonly isOwner = computed(() => this.me()?.role === 'owner');
  protected readonly name = computed(() => {
    const me = this.me();
    return me?.display_name || me?.username || '';
  });

  ngOnInit(): void {
    forkJoin({ me: this.api.me(), apps: this.people.myApps() }).subscribe({
      next: ({ me, apps }) => {
        this.me.set(me);
        this.sessions.role.set(me.role ?? null);
        this.apps.set(apps.apps ?? []);
        this.loading.set(false);
      },
      error: () => {
        this.failed.set(true);
        this.loading.set(false);
      },
    });
  }

  protected signOut(): void {
    this.sessions.logout().subscribe(() => void this.router.navigateByUrl('/login'));
  }
}
