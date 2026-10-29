import { Component, input } from '@angular/core';
import { RouterLink } from '@angular/router';
import { IssuerMovePlan } from '../core/api.service';

/**
 * The apps whose sign-in moves to Authentik's public address with a
 * certificate for the public dashboard name (S325, ADR-048 §6): each
 * app's issuer change and the host entry its containers get. Without the
 * kept Authentik token nothing is re-applied, so each links to its card
 * to re-plan sign-in.
 */
@Component({
  selector: 'app-issuer-moves',
  imports: [RouterLink],
  template: `
    @if (moves(); as m) {
      @if (m.apps.length) {
        <section class="issuer-moves" aria-label="Apps moving to Authentik's public address">
          <h3>Sign-in moves to Authentik's public address</h3>
          <p>{{ m.note }}</p>
          <ul>
            @for (app of m.apps; track app.app) {
              <li>
                <strong>{{ app.title }}</strong>: signs in at <span class="mono">{{ app.to }}</span>
                instead of <span class="mono">{{ app.from }}</span>. Its containers get the host entry
                <span class="mono">{{ app.host_entry }}</span>@if (app.replaced_host_entry) {, replacing
                  <span class="mono">{{ app.replaced_host_entry }}</span>}.
                @if (!m.token_kept) {
                  <a routerLink="/admin/catalog" [fragment]="'manage-' + app.app">Re-plan {{ app.title }} sign-in</a>
                }
              </li>
            }
          </ul>
        </section>
      }
    }
  `,
})
export class IssuerMoves {
  readonly moves = input<IssuerMovePlan | undefined>(undefined);
}
