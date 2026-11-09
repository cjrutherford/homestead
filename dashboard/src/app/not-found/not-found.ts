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
      <section class="field-panel" aria-label="Where next">
        <p><a routerLink="/admin/status">Back to status</a></p>
      </section>
    </section>
  `,
})
export class NotFound {}
