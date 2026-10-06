import { Component, inject } from '@angular/core';
import { RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { SessionService } from './auth/session.service';

/** Homestead dashboard shell: brand header, primary nav, routed content. */
@Component({
  imports: [RouterOutlet, RouterLink, RouterLinkActive],
  selector: 'app-root',
  styleUrl: './app.css',
  templateUrl: './app.html',
})
export class App {
  protected readonly sessions = inject(SessionService);

  protected logout(): void {
    this.sessions.logout().subscribe();
  }
}
