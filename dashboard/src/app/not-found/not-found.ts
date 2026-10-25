import { Component } from '@angular/core';
import { RouterLink } from '@angular/router';

/** Fallback page for unknown routes. */
@Component({
  imports: [RouterLink],
  selector: 'app-not-found',
  template: `
    <section aria-label="Not found">
      <header class="masthead">
        <h1>Not found</h1>
        <p class="masthead-fact">This page does not exist.</p>
      </header>
      <a routerLink="/status">Back to status</a>
    </section>
  `,
})
export class NotFound {}
