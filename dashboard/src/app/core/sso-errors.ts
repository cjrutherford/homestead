/**
 * Fixed sign-in failure codes the daemon puts on the redirect back to the
 * login page (ADR-033). The codes carry no detail; the words live here.
 */
const MESSAGES: Record<string, string> = {
  unavailable: 'Authentik did not answer. Use your local password.',
  unlinked: 'That Authentik account is not linked to this appliance. Use your local password.',
  state: 'That sign-in expired or was already used. Start again, or use your local password.',
  origin:
    'Sign-in with Authentik is not set up for this address. Open Homestead at one of the addresses on the Sign-in page, or use your local password.',
  'rate-limited': 'Too many sign-in attempts. Wait a minute, or use your local password.',
  'link-conflict': 'An Authentik account is already linked. Unlink it first.',
};

/** The sentence for a failure code, or null when there is none. */
export function ssoErrorMessage(code: string | null): string | null {
  if (!code) {
    return null;
  }
  return MESSAGES[code] ?? 'Sign-in with Authentik did not finish. Use your local password.';
}
