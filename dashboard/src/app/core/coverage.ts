import { NameCoverage } from './api.service';

/**
 * The edge certificate lamp for a public name (S312): does a Cloudflare
 * certificate cover it, so browsers can connect? Names one level below the
 * zone are covered for free; deeper names need Advanced Certificate Manager.
 */
export interface CoverageLamp {
  ok: boolean;
  label: string;
  title: string;
}

export function coverageLamp(coverage: NameCoverage[] | undefined, hostname: string): CoverageLamp | undefined {
  const name = hostname.trim().toLowerCase().replace(/\.$/, '');
  const c = (coverage ?? []).find((entry) => entry.hostname.toLowerCase() === name);
  if (!c) {
    return undefined;
  }
  const unchecked = c.checked ? '' : " The token cannot read certificates, so only the free certificate's rule was checked.";
  if (c.covered) {
    return { ok: true, label: 'certificate covers it', title: `Covered (${c.by || 'certificate'}).${unchecked}` };
  }
  return {
    ok: false,
    label: 'no certificate yet',
    title: `Browsers will fail to connect until Cloudflare has a certificate for ${name}. Names more than one level below the zone need Advanced Certificate Manager.${unchecked}`,
  };
}

/** The rule, said once wherever a public name is chosen. */
export const coverageRule =
  "Names one level below your domain (such as photos.example.com) are covered by Cloudflare's free certificate. " +
  'Deeper names (such as photos.homestead.example.com) need Advanced Certificate Manager on the zone; the review says which.';
