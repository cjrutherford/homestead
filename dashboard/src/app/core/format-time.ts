import { Pipe, PipeTransform } from '@angular/core';

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

/**
 * Shows an RFC 3339 timestamp as "19 Oct 2026, 19:18 UTC" (S284 KVM review: raw
 * nanosecond timestamps wrapped mid-token in tables). Always UTC so the text is
 * the same on every device; the full value stays in the `datetime` attribute.
 * Anything that does not parse, and Go's zero time, is returned as given
 * (or the fallback for the zero time).
 */
export function formatWhen(iso: string | null | undefined, empty = ''): string {
  if (!iso || iso.startsWith('0001-')) {
    return empty;
  }
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) {
    return iso;
  }
  const hh = String(d.getUTCHours()).padStart(2, '0');
  const mm = String(d.getUTCMinutes()).padStart(2, '0');
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}, ${hh}:${mm} UTC`;
}

/** Upper-cases the first letter so a daemon sentence fragment reads as a sentence. */
export function sentence(text: string | null | undefined): string {
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : '';
}

@Pipe({ name: 'when' })
export class WhenPipe implements PipeTransform {
  transform(iso: string | null | undefined, empty = ''): string {
    return formatWhen(iso, empty);
  }
}

@Pipe({ name: 'sentence' })
export class SentencePipe implements PipeTransform {
  transform(text: string | null | undefined): string {
    return sentence(text);
  }
}
