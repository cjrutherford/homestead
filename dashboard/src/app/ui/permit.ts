import { Component, computed, input } from '@angular/core';
import { WhenPipe } from '../core/format-time';

/**
 * Who approved a plan and when (RFC 3339), shown on the stamp. Both are
 * optional: a plan the appliance reports as approved, without saying by
 * whom, still gets its stamp.
 */
export interface Approval {
  by?: string;
  at?: string;
}

/** The approval the owner just gave: their name (or "you") and now. */
export function approvedNow(by: string | null | undefined): Approval {
  return { by: by || 'you', at: new Date().toISOString() };
}

/** A plan the owner approved here, kept on the page as its stamped permit. */
export interface ApprovedPlan {
  heading: string;
  digest: string;
  summary: string;
  approval: Approval;
}

/** Records the approval of a reviewed plan, now, by the signed-in owner. */
export function approvedPlan(heading: string, digest: string, summary: string, by: string | null | undefined): ApprovedPlan {
  return { heading, digest, summary, approval: approvedNow(by) };
}

let nextId = 0;

/** The first eight characters of a digest, without an algorithm prefix. */
export function sealCode(digest: string): string {
  const bare = digest.includes(':') ? digest.slice(digest.indexOf(':') + 1) : digest;
  return bare.slice(0, 8);
}

/**
 * The permit (S309): every plan the owner reviews and approves renders as a
 * paper document. The title is in the display face, the plan digest sits in
 * the corner as a seal (the full digest is its accessible name), and the
 * page's own review content is projected into the body. Pass `approval`
 * once the plan was approved and the Approved stamp presses on; pass
 * `refusal` when the appliance would not approve it and the Not approved
 * stamp states the reason. The page keeps its own actions and state.
 */
@Component({
  selector: 'app-permit',
  imports: [WhenPipe],
  template: `
    <article
      class="permit"
      [attr.data-state]="state()"
      [attr.aria-label]="label() || null"
      [attr.aria-labelledby]="label() ? null : titleId"
    >
      <header class="permit-head">
        <div class="permit-heading">
          <p class="permit-kind">{{ kind() }}</p>
          @if (level() === 2) {
            <h2 class="permit-title" [id]="titleId">{{ heading() }}</h2>
          } @else {
            <h3 class="permit-title" [id]="titleId">{{ heading() }}</h3>
          }
        </div>
        @if (digest(); as full) {
          <span class="permit-seal" role="img" [attr.aria-label]="'Plan digest ' + full">
            <span class="permit-seal-label" aria-hidden="true">Digest</span>
            <span class="permit-seal-code" aria-hidden="true">{{ code() }}</span>
          </span>
        }
      </header>
      <div class="permit-body">
        <ng-content />
      </div>
      @if (approval(); as stamp) {
        <p class="permit-stamp" data-kind="approved">
          <span class="permit-stamp-word">Approved</span>
          @if (stamp.by) {
            <span class="permit-stamp-meta">by {{ stamp.by }}@if (stamp.at) {, {{ stamp.at | when }}}</span>
          } @else if (stamp.at) {
            <span class="permit-stamp-meta">{{ stamp.at | when }}</span>
          }
        </p>
      } @else if (refusal(); as reason) {
        <div class="permit-refusal">
          <p class="permit-stamp" data-kind="refused"><span class="permit-stamp-word">Not approved</span></p>
          <p class="permit-reason">{{ reason }}</p>
        </div>
      }
    </article>
  `,
})
export class Permit {
  readonly heading = input.required<string>();
  readonly digest = input<string | null | undefined>(null);
  /** Heading level of the title: 3 inside a page section (default), 2 when the permit is the section. */
  readonly level = input<2 | 3>(3);
  /** An accessible name for the permit, when the page already names its review region. */
  readonly label = input<string | null>(null);
  readonly approval = input<Approval | null | undefined>(null);
  readonly refusal = input<string | null | undefined>(null);

  protected readonly titleId = `permit-title-${++nextId}`;
  protected readonly code = computed(() => sealCode(this.digest() ?? ''));
  protected readonly state = computed(() =>
    this.approval() ? 'approved' : this.refusal() ? 'refused' : 'pending',
  );
  protected readonly kind = computed(() => {
    switch (this.state()) {
      case 'approved':
        return 'Approved plan';
      case 'refused':
        return 'Plan not approved';
      default:
        return 'Plan for your approval';
    }
  });
}
