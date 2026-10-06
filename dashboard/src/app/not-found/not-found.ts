import { Component } from '@angular/core';
import { RouterLink } from '@angular/router';

/** Fallback page for unknown routes. */
@Component({
  imports: [RouterLink],
  selector: 'app-not-found',
  template: `
    <section aria-label="Not found">
      <h2>Not found</h2>
      <p>This page does not exist.</p>
      <a routerLink="/status">Back to status</a>
    </section>
  `,
})
export class NotFound {}
